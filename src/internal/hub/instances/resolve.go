package instances

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/internal/hub/proxies"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// issueKind 区分实例无法运行的原因，决定展示状态：
// 插件消失、代理不可用、密钥无法解密等是 broken（引用失效）；
// 配置与当前插件 schema 不符（含复制后待补的密钥）是 unconfigured（需要用户填写）。
type issueKind int

const (
	issueBroken issueKind = iota + 1
	issueUnconfigured
)

type issue struct {
	kind     issueKind
	msg      string
	problems model.FieldErrors
}

// resolved 是一个可运行实例的完整输入：当前插件、整理后的完整配置及其拆分结果。
type resolved struct {
	plugin  plugins.Plugin
	fields  []schema.Field
	cfg     map[string]any
	plain   map[string]any
	secrets map[string]any
}

// loadFull 取实例的插件并还原完整配置（普通配置 + 解密后的密钥），
// 丢弃插件升级后已不再声明的键（记日志，不落库），不做校验。
func (s *Service) loadFull(r row) (plugins.Plugin, map[string]any, *issue) {
	p, ok := s.reg.Get(r.PluginID)
	if !ok {
		return plugins.Plugin{}, nil, &issue{kind: issueBroken, msg: fmt.Sprintf("插件 %s 不可用", r.PluginID)}
	}
	secrets, err := s.decodeSecrets(r.SecretsEnc)
	if err != nil {
		return p, nil, &issue{kind: issueBroken, msg: err.Error()}
	}
	fields := p.Manifest.ConfigSchema
	var dropped []string
	full := pruneUndeclared(fields, schema.Merge(fields, r.Config, secrets), "", &dropped)
	if len(dropped) > 0 && s.firstDropReport(r.ID, strings.Join(dropped, ",")) {
		s.log.Warn("实例配置含插件已不再声明的键，已忽略", "instance", r.ID, "plugin", r.PluginID, "keys", dropped)
	}
	return p, full, nil
}

// resolve 在 loadFull 之后做完整校验（补默认值、丢弃不可见字段）。
func (s *Service) resolve(r row) (*resolved, *issue) {
	p, full, iss := s.loadFull(r)
	if iss != nil {
		return nil, iss
	}
	fields := p.Manifest.ConfigSchema
	cfg, errs := schema.Prepare(fields, full)
	if len(errs) > 0 {
		return nil, &issue{kind: issueUnconfigured, msg: "配置不完整或与当前插件不符: " + errs.Error(), problems: errs}
	}
	plain, secrets := schema.Split(fields, cfg)
	return &resolved{plugin: p, fields: fields, cfg: cfg, plain: plain, secrets: secrets}, nil
}

// pruneUndeclared 递归丢弃 schema 未声明的键：顶层与 object_list 元素内。
func pruneUndeclared(fields []schema.Field, cfg map[string]any, prefix string, dropped *[]string) map[string]any {
	declared := make(map[string]*schema.Field, len(fields))
	for i := range fields {
		declared[fields[i].Key] = &fields[i]
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		f, ok := declared[k]
		if !ok {
			*dropped = append(*dropped, path)
			continue
		}
		if list, isList := v.([]any); isList && f.Type == schema.TypeObjectList {
			nl := make([]any, len(list))
			for i, e := range list {
				if m, isMap := e.(map[string]any); isMap {
					nl[i] = pruneUndeclared(f.Fields, m, fmt.Sprintf("%s[%d]", path, i), dropped)
				} else {
					nl[i] = e
				}
			}
			out[k] = nl
			continue
		}
		out[k] = v
	}
	slices.Sort(*dropped)
	return out
}

// sourceFor 取插件的 Source：内置直接用，exec 按目录现造。
func sourceFor(p plugins.Plugin) runtime.Source {
	if p.Origin == plugins.OriginExec {
		return runtime.NewExecSource(p.Manifest, p.RunPath)
	}
	return p.Source
}

// effectiveInterval 是实际生效的刷新间隔：覆盖值优先，否则取 manifest。
func effectiveInterval(r row, m *manifest.Manifest) time.Duration {
	if r.IntervalSeconds > 0 {
		return time.Duration(r.IntervalSeconds) * time.Second
	}
	if m != nil {
		return m.Interval
	}
	return 0
}

// resolveProxy 解析实例使用的代理；代理已被删除时按直连运行并记日志（Ruling 28）。
// 其它失败（如认证无法解密）返回错误，调用方不得退回直连，以免暴露出口 IP。
func (s *Service) resolveProxy(ctx context.Context, instanceID, proxyID string) (*proxy.Proxy, error) {
	if proxyID == "" || strings.EqualFold(proxyID, proxy.DirectValue) {
		return nil, nil
	}
	if s.proxies == nil {
		return nil, errors.New("代理仓库未接入")
	}
	p, err := s.proxies.Resolve(ctx, proxyID)
	if errors.Is(err, proxies.ErrNotFound) {
		s.log.Warn("实例引用的代理已不存在，按直连运行", "instance", instanceID, "proxy", proxyID)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("代理 %s 不可用: %w", proxyID, err)
	}
	return p, nil
}

// baseInput 构造采集输入的固定部分；Last 与 State 在每次运行时再填。
func (s *Service) baseInput(res *resolved, pr *proxy.Proxy) runtime.Input {
	sec := make(map[string]string, len(res.secrets))
	for k, v := range res.secrets {
		if str, ok := v.(string); ok {
			sec[k] = str
		}
	}
	return runtime.Input{Config: res.plain, Secrets: sec, Proxy: pr, Clock: s.clk}
}

// runtimeHash 是调度器的 ConfigHash：把影响运行的一切折进去——完整配置（含密钥）、
// 插件版本与来源、间隔、超时、代理。只在内存里使用，不落库、不写日志。
func runtimeHash(res *resolved, interval time.Duration, pr *proxy.Proxy) string {
	cfg, _ := json.Marshal(res.cfg)
	h := sha256.New()
	for _, part := range []string{
		string(cfg), res.plugin.Manifest.Version, string(res.plugin.Origin), res.plugin.RunPath,
		interval.String(), res.plugin.Manifest.Timeout.String(), pr.URL(),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// firstDropReport 同一实例同一组被忽略的键只报告一次（详情、列表、调度都会走到这里）。
func (s *Service) firstDropReport(id, keys string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dropWarned[id] == keys {
		return false
	}
	s.dropWarned[id] = keys
	return true
}
