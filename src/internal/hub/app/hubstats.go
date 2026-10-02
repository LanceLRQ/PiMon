package app

import (
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/history"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// hubStats 把中枢内部计数适配给 hub-self 插件（hubself.Stats）。
type hubStats struct {
	inst    *instances.Service
	hist    *history.Service
	started time.Time
	dataDir string
	// screenOnline 报告是否有屏幕会话在线；为空表示来源未接入。
	screenOnline func() (online, known bool)
	// screenStatus 返回当前屏幕与 kiosk 状态；为空表示来源未接入。
	screenStatus func() model.ScreenStatus
}

// WriteErrors 是当前状态落盘与历史写盘失败次数之和。
func (s hubStats) WriteErrors() int64 { return s.inst.WriteErrors() + s.hist.WriteErrors() }

// OnlineAgents 在 M2 接入 agent 之前恒为 0。
func (hubStats) OnlineAgents() int { return 0 }

// ScreenOnline 取自屏幕会话的在线状态；来源未接入时为未知（known=false），不当作离线（Ruling 50）。
func (s hubStats) ScreenOnline() (online, known bool) {
	if s.screenOnline == nil {
		return false, false
	}
	return s.screenOnline()
}

// PushFailures 在 M4 接入推送之前恒为 0。
func (hubStats) PushFailures() int64 { return 0 }

func (s hubStats) StartedAt() time.Time { return s.started }

func (s hubStats) DataDir() string { return s.dataDir }

// KioskChromiumRSS 取自最近一次 kiosk 上报；来源未接入时为未知。
func (s hubStats) KioskChromiumRSS() (int64, bool) {
	if s.screenStatus == nil {
		return 0, false
	}
	return kioskRSS(s.screenStatus())
}

// kioskRSS 仅在 kiosk 在线且上报了 Chromium RSS 时返回值：
// 从未上报、离线（保留的是旧值）或值为空都是未知，不当作 0。
func kioskRSS(st model.ScreenStatus) (int64, bool) {
	k := st.Kiosk
	if k == nil || !k.Online || k.ChromiumRSSBytes == nil {
		return 0, false
	}
	return *k.ChromiumRSSBytes, true
}
