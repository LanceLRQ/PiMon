package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// maxReceiptMessage 是 assign_result 里 Message 的最大字节数。
const maxReceiptMessage = 512

// spec 是当前 assign 里的一个实例：回执结果，以及回执为 started 时的运行快照。
type spec struct {
	status  string
	message string
	// job 只在 status 为 started 时非 nil。
	job *job
	// localHash 是本地任务哈希：下发摘要 + 本地插件来源、RunPath、manifest 版本
	// （同一下发摘要下本地插件换了实现也要重排）。
	localHash string
	stream    bool
}

// reason 是回执失败时给 run_now 的错误文字（只含固定文字与字段路径）。
func (sp *spec) reason() string {
	if sp.message == "" {
		return sp.status
	}
	return sp.status + ": " + sp.message
}

// Assign 应用一次全量下发：逐实例校验并汇总成 assign_result（按下发顺序，Version 原样回显），
// 与当前集合对比——移出或回执失败的停止，新增或本地哈希变化的重新登记，未变的不动。
// 移出 assign 不删除 state；出现在 assign 里的实例刷新分配时间（见 Runner.GC）。
// 在会话 Handler 里同步调用，之后的 run_now 一定看到本次下发；Binding 已关闭时忽略。
func (b *Binding) Assign(m *agent.Assign) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	next := make(map[string]*spec, len(m.Instances))
	ids := make(map[string]bool, len(m.Instances))
	touch := make([]string, 0, len(m.Instances))
	results := make([]agent.AssignResultItem, 0, len(m.Instances))
	for _, in := range m.Instances {
		sp := b.prepare(in)
		next[in.ID] = sp
		ids[in.ID] = true
		touch = append(touch, in.ID)
		results = append(results, agent.AssignResultItem{InstanceID: in.ID, Status: sp.status, Message: sp.message})
	}
	if err := b.r.cfg.Store.Touch(b.ctx, touch); err != nil {
		b.r.cfg.Log.Warn("刷新 state 分配时间失败", "err", err)
	}

	for id, old := range b.specs {
		nw := next[id]
		if old.job != nil && (nw == nil || nw.job == nil || nw.stream != old.stream) {
			b.dropTask(id, old)
		}
	}
	started := 0
	for _, sp := range next {
		if sp.job != nil {
			b.upsertTask(sp)
			started++
		}
	}
	b.specs = next
	b.r.setAssigned(b, ids)
	b.r.cfg.Log.Info("已应用 assign", "version", m.Version, "instances", len(m.Instances), "started", started)
	b.send(&agent.AssignResult{Version: m.Version, Results: results})
}

// prepare 校验一个下发实例并构造运行快照。校验顺序：插件存在 → manifest 兼容 → 代理与配置合法。
func (b *Binding) prepare(in agent.AssignedInstance) *spec {
	p, ok := b.r.cfg.Registry.Get(in.PluginID)
	if !ok {
		return &spec{status: agent.AssignPluginMissing}
	}
	m := p.Manifest
	switch {
	case m.APIVersion != manifest.SupportedAPIVersion:
		return &spec{status: agent.AssignIncompatible, message: "api_version 不受支持 / unsupported api_version"}
	case m.Kind != manifest.KindSource:
		return &spec{status: agent.AssignIncompatible, message: "不是数据源插件 / not a source plugin"}
	case !slices.Contains(m.RunsOn, manifest.RunsOnAgent):
		return &spec{status: agent.AssignIncompatible, message: "插件不能在 agent 上运行 / plugin does not run on agent"}
	}
	pr, err := proxy.Parse(in.ProxyURL)
	if err != nil {
		// proxy.Parse 的错误可能带协议名等原文，回执只给字段路径。
		return &spec{status: agent.AssignInvalidConfig, message: "proxy_url:invalid"}
	}
	secrets := make(map[string]any, len(in.Secrets))
	for k, v := range in.Secrets {
		secrets[k] = v
	}
	if errs := schema.Validate(m.ConfigSchema, schema.Merge(m.ConfigSchema, in.Config, secrets)); len(errs) > 0 {
		return &spec{status: agent.AssignInvalidConfig, message: fieldErrorsText(errs)}
	}

	interval := time.Duration(in.IntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = m.Interval
	}
	_, isStream := p.Source.(runtime.Streamer)
	return &spec{
		status: agent.AssignStarted,
		job: &job{
			id: in.ID, pluginID: in.PluginID, hash: in.ConfigHash, src: p.Source,
			in:       runtime.Input{Config: in.Config, Secrets: in.Secrets, Proxy: pr, Clock: b.r.cfg.Clock},
			interval: interval,
			timeout:  time.Duration(in.TimeoutMs) * time.Millisecond,
		},
		localHash: localHash(in.ConfigHash, p),
		stream:    isStream,
	}
}

// fieldErrorsText 把配置校验错误写成「字段路径:错误码」，按字段排序，截到 512 字节；不含配置值。
func fieldErrorsText(errs map[string]string) string {
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+":"+errs[k])
	}
	s := strings.Join(parts, ", ")
	if len(s) > maxReceiptMessage {
		s = strings.ToValidUTF8(s[:maxReceiptMessage], "")
	}
	return s
}

// localHash 折入下发摘要与本地插件身份；只在内存使用。
func localHash(configHash string, p plugins.Plugin) string {
	h := sha256.New()
	for _, part := range []string{configHash, p.Origin, p.RunPath, p.Manifest.Version} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// upsertTask 把实例登记到调度器或 Streamer 管理器（本地哈希不变时两者都不重排）。持 b.mu 调用。
func (b *Binding) upsertTask(sp *spec) {
	j := sp.job
	if sp.stream {
		b.streams.Upsert(runtime.StreamTask{
			ID: j.id, ConfigHash: sp.localHash, Input: j.in,
			Streamer: &streamRun{b: b, j: j, inner: j.src.(runtime.Streamer)},
		})
		return
	}
	b.sched.Upsert(runtime.Task{ID: j.id, Interval: j.interval, ConfigHash: sp.localHash, Run: b.periodicRun(j)})
}

// dropTask 停止实例（取消运行中的一次）。持 b.mu 调用。
func (b *Binding) dropTask(id string, sp *spec) {
	if sp.stream {
		b.streams.Remove(id)
	} else {
		b.sched.Remove(id)
	}
}
