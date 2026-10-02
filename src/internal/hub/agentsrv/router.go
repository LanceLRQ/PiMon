package agentsrv

import (
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// Router 接收 hello 被接受之后的业务事件（下发器与报告处理在 B6 实现）。
// 回调在会话的分发协程中同步调用，不得长时间阻塞（收件队列满会断开会话），也不得等待会话本身的请求应答。
type Router interface {
	// OnReady 在设备 hello 被接受（hello_ok 已发出、已标记在线）后调用，
	// 以及收到 plugins_changed 并重新入库 manifest 后调用；实现应向该设备全量下发实例。
	OnReady(deviceID string)
	// OnReport 收到周期性采集结果。
	OnReport(deviceID string, m *agent.Report)
	// OnAssignResult 收到 assign 的逐实例回执。
	OnAssignResult(deviceID string, m *agent.AssignResult)
	// OnUpgradeInfoGet 收到 agent 的升级信息请求，实现经 s 回 upgrade_info。
	OnUpgradeInfoGet(s *agentlink.Session, deviceID string, m *agent.UpgradeInfoGet)
}

// NopRouter 是空实现：忽略所有事件。
type NopRouter struct{}

// OnReady 实现 Router。
func (NopRouter) OnReady(string) {}

// OnReport 实现 Router。
func (NopRouter) OnReport(string, *agent.Report) {}

// OnAssignResult 实现 Router。
func (NopRouter) OnAssignResult(string, *agent.AssignResult) {}

// OnUpgradeInfoGet 实现 Router。
func (NopRouter) OnUpgradeInfoGet(*agentlink.Session, string, *agent.UpgradeInfoGet) {}
