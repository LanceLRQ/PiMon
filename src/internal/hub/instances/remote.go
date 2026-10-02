package instances

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

// RemoteSpec 是下发给设备的一个实例：已解析的普通配置、解密后的密钥、代理 URL、有效间隔与超时。
// Secrets 与 ProxyURL 含敏感信息，不得写日志（String/LogValue 已脱敏）。
type RemoteSpec struct {
	ID       string
	PluginID string
	Config   map[string]any
	Secrets  map[string]string
	// ProxyURL 是解析后的代理 URL，直连为空。
	ProxyURL string
	Interval time.Duration
	Timeout  time.Duration
	// ConfigHash 是下发内容的摘要（remoteHash），agent 回报时据此判断结果是否对应当前配置；不落库、不写日志。
	ConfigHash string
}

// String 只输出 id 与插件，不含配置、密钥与代理。
func (r RemoteSpec) String() string {
	return fmt.Sprintf("RemoteSpec{id:%s plugin_id:%s}", r.ID, r.PluginID)
}

// LogValue 与 String 一致，避免 slog 展开结构体。
func (r RemoteSpec) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", r.ID), slog.String("plugin_id", r.PluginID))
}

// proxyRemovedTag 是代理已删除时参与下发摘要的代理标记。
const proxyRemovedTag = "!removed"

// RemoteSpecs 返回运行在 machine（设备 ID）上、当前可以运行的全部实例，按名称排序，以库为准。
// 暂停、插件不可用、配置不完整或损坏的实例不在结果里（原因由实例视图实时计算）；
// 代理不可用的记为实例问题；代理已被删除的不下发，按下发摘要去重只记一次失败（与本机 Streamer 一致）。
func (s *Service) RemoteSpecs(ctx context.Context, machine string) ([]RemoteSpec, error) {
	if machine == "" || machine == machineHub {
		return nil, errors.New("RemoteSpecs 只用于设备")
	}
	rows, err := s.queryRows(ctx, `SELECT `+rowCols+` FROM plugin_instances WHERE machine = ? ORDER BY name, id`, machine)
	if err != nil {
		return nil, err
	}
	out := make([]RemoteSpec, 0, len(rows))
	for _, r := range rows {
		if r.Paused {
			continue
		}
		res, iss := s.resolve(r)
		if iss != nil {
			continue
		}
		pr, proxyFail, err := s.proxyForRun(ctx, r)
		if err != nil {
			if s.swapSyncIssue(r.ID, &issue{kind: issueBroken, msg: err.Error()}) {
				s.notify(r.ID)
			}
			continue
		}
		if s.swapSyncIssue(r.ID, nil) {
			s.notify(r.ID)
		}
		interval := effectiveInterval(r, res.plugin.Manifest)
		timeout := res.plugin.Manifest.Timeout
		if timeout <= 0 {
			timeout = manifest.DefaultTimeout
		}
		if proxyFail != nil {
			if s.firstProxyFail(r.ID, remoteHash(r.ConfigHash, res, interval, timeout, proxyRemovedTag)) {
				s.applyResult(r.ID, r.ConfigHash, nil, proxyFail)
			}
			continue
		}
		s.forgetProxyFail(r.ID)
		in := s.baseInput(res, pr)
		out = append(out, RemoteSpec{
			ID: r.ID, PluginID: r.PluginID, Config: res.plain, Secrets: in.Secrets, ProxyURL: pr.URL(),
			Interval: interval, Timeout: timeout,
			ConfigHash: remoteHash(r.ConfigHash, res, interval, timeout, pr.URL()),
		})
	}
	return out, nil
}

// remoteHash 是下发给设备的实例摘要：输入与 runtimeHash 相同，但去掉只对本机有意义的 RunPath 与来源，
// 插件版本取设备上报的 manifest。含明文配置与密钥，只经通道发给 agent，不落库、不写日志。
func remoteHash(content string, res *resolved, interval, timeout time.Duration, proxyURL string) string {
	cfg, _ := json.Marshal(res.cfg)
	h := sha256.New()
	for _, part := range []string{
		content, string(cfg), res.plugin.Manifest.Version, interval.String(), timeout.String(), proxyURL,
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SetAssignIssue 记录 machine 对实例下发的回执问题（msg 为空表示清除），按引用失效展示；
// 只在实例仍运行在 machine 且未暂停时展示，按设备分别记录，旧设备迟到的回执不会覆盖当前设备的问题。
// 内容有变化时通知订阅者。实例不存在时忽略。
func (s *Service) SetAssignIssue(machine, id, msg string) {
	s.mu.Lock()
	byMachine := s.assignIssues[id]
	cur, had := byMachine[machine]
	changed := false
	switch {
	case msg == "":
		if had {
			delete(byMachine, machine)
			if len(byMachine) == 0 {
				delete(s.assignIssues, id)
			}
			changed = true
		}
	case s.states[id] == nil:
		// 实例已被删除（或尚未加载），不留残余条目。
	case !had || cur != msg:
		if byMachine == nil {
			byMachine = map[string]string{}
			s.assignIssues[id] = byMachine
		}
		byMachine[machine] = msg
		changed = true
	}
	s.mu.Unlock()
	if changed {
		s.notify(id)
	}
}

// assignIssueFor 返回实例当前有效的回执问题。调用方须持有 s.mu。
func (s *Service) assignIssueFor(r row) *issue {
	msg, ok := s.assignIssues[r.ID][r.Machine]
	if !ok || r.Paused {
		return nil
	}
	return &issue{kind: issueBroken, msg: msg}
}

// swapSyncIssue 设置或清除实例的排程问题，返回是否有变化。
func (s *Service) swapSyncIssue(id string, iss *issue) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, had := s.syncIssues[id]
	if iss == nil {
		delete(s.syncIssues, id)
		return had
	}
	s.syncIssues[id] = iss
	return !had || cur.msg != iss.msg
}

// firstProxyFail 同一实例同一下发摘要的「代理已删除」只记一次失败。
func (s *Service) firstProxyFail(id, hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proxyFailed[id] == hash {
		return false
	}
	s.proxyFailed[id] = hash
	return true
}

func (s *Service) forgetProxyFail(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.proxyFailed, id)
}
