package runner

import (
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// run_now 的固定错误文字。
const (
	errTextBusy        = "实例正在运行 / instance is running"
	errTextNotAssigned = "实例未下发到本机 / instance not assigned to this agent"
)

// RunNow 处理 hub 的立即运行请求：在会话 Handler 里同步取当前下发的快照——不在 assign 里回 not_assigned，
// 回执失败的回 failed（Error 为回执原因），实例运行锁被占用时立即回 busy（不等待：hub 只等插件超时 + 5 秒）；
// 拿到锁后另起协程采集、写 state，结果只以 run_result 回复（RequestID 原样回显），不再发 report。
// Binding 已关闭时忽略。
func (b *Binding) RunNow(m *agent.RunNow) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	sp := b.specs[m.InstanceID]
	if sp == nil || sp.job == nil {
		b.mu.Unlock()
		o := agent.RunOutcome{Error: errTextNotAssigned, ErrorKind: agent.ErrorKindNotAssigned, CollectedAt: b.r.cfg.Clock.Now()}
		if sp != nil {
			o.Error, o.ErrorKind = sp.reason(), agent.ErrorKindFailed
		}
		b.send(&agent.RunResult{RequestID: m.RequestID, InstanceID: m.InstanceID, RunOutcome: o})
		return
	}
	j := sp.job
	release, ok := b.r.tryAcquire(j.id)
	if !ok {
		b.mu.Unlock()
		b.send(&agent.RunResult{RequestID: m.RequestID, InstanceID: j.id, RunOutcome: agent.RunOutcome{
			Error: errTextBusy, ErrorKind: agent.ErrorKindBusy, CollectedAt: b.r.cfg.Clock.Now(), ConfigHash: j.hash,
		}})
		return
	}
	// 在 b.mu 内登记，保证 Close 的 wg.Wait 一定能等到它。
	b.wg.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.wg.Done()
		res := b.collect(b.ctx, j)
		release()
		if res.canceled {
			return
		}
		b.send(&agent.RunResult{RequestID: m.RequestID, InstanceID: j.id, RunOutcome: res.outcome(j.hash)})
	}()
}
