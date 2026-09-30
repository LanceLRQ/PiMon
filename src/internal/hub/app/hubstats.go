package app

import (
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/history"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
)

// hubStats 把中枢内部计数适配给 hub-self 插件（hubself.Stats）。
type hubStats struct {
	inst    *instances.Service
	hist    *history.Service
	started time.Time
	dataDir string
}

// WriteErrors 是当前状态落盘与历史写盘失败次数之和。
func (s hubStats) WriteErrors() int64 { return s.inst.WriteErrors() + s.hist.WriteErrors() }

// OnlineAgents 在 M2 接入 agent 之前恒为 0。
func (hubStats) OnlineAgents() int { return 0 }

// ScreenOnline 在 M1c 接入屏幕会话之前恒为未知（known=false），不当作离线（Ruling 50）。
func (hubStats) ScreenOnline() (online, known bool) { return false, false }

// PushFailures 在 M4 接入推送之前恒为 0。
func (hubStats) PushFailures() int64 { return 0 }

func (s hubStats) StartedAt() time.Time { return s.started }

func (s hubStats) DataDir() string { return s.dataDir }
