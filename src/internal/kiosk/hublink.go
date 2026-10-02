package kiosk

import (
	"context"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// LinkSink 是守护进程交给 HubLink 的回调集合；HubLink 的实现在对应事件发生时调用它。
// 所有方法可在任意 goroutine 调用；守护进程退出后调用立即返回。
type LinkSink interface {
	// OnConnected 在每次（重新）连上 hub 后调用，守护进程随即上报当前状态。
	OnConnected()
	// OnSettings 在收到设置（首个 snapshot 的 screen_settings 或后续 settings patch）时调用。
	OnSettings(ui.ScreenSettings)
	// OnBuild 在收到 snapshot 时调用，参数是 hub 的构建版本。
	OnBuild(build string)
	// ReadToken 读取并返回当前屏幕令牌；每次（重）连接前必须重新调用，不得缓存旧令牌。
	ReadToken() (string, error)
}

// HubLink 是 kiosk 与 hub 的连接，真实实现（WebSocket 客户端）由后续任务提供。
type HubLink interface {
	// Run 连接 hub 并维持连接（断线按退避重连），事件交给 sink；ctx 结束时返回。
	Run(ctx context.Context, sink LinkSink)
	// Report 上报 kiosk 状态（整份覆盖）。必须不阻塞；未连接时丢弃即可。
	Report(model.KioskReport)
}

// NopLink 是不连接 hub 的空实现：守护进程照常看护 Chromium，缩放按 1、不做每日重启。
type NopLink struct{}

// Run 阻塞到 ctx 结束。
func (NopLink) Run(ctx context.Context, _ LinkSink) { <-ctx.Done() }

// Report 丢弃上报。
func (NopLink) Report(model.KioskReport) {}
