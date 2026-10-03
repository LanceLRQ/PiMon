package devices

import (
	"context"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// ViewerConfig 是设备视图的依赖。ConnError 与 ClockSkew 是运行时值的来源（拨出管理器、agent 通道服务），
// 为 nil 表示没有该来源。
type ViewerConfig struct {
	Store    *Store
	Presence *Presence
	// ConnError 返回设备最近的连接错误，没有时为空串。
	ConnError func(id string) string
	// ClockSkew 返回设备会话的时钟偏移（对端 − hub），没有样本时 ok 为假。
	ClockSkew func(id string) (skew time.Duration, ok bool)
}

// Viewer 把设备行与内存里的运行时值（在线状态、连接错误、时钟偏移）拼成对外的 model.Device，
// 供设备 REST 接口与 WS 推送共用。不持有锁，可并发调用。
type Viewer struct{ cfg ViewerConfig }

// NewViewer 创建设备视图。
func NewViewer(cfg ViewerConfig) *Viewer { return &Viewer{cfg: cfg} }

// View 读取单个设备；不存在返回 ErrNotFound。
func (v *Viewer) View(ctx context.Context, id string) (model.Device, error) {
	d, err := v.cfg.Store.Get(ctx, id)
	if err != nil {
		return model.Device{}, err
	}
	v.fill(&d)
	return d, nil
}

// List 返回全部设备（不含中枢本机，由前端合成固定首行）。
func (v *Viewer) List(ctx context.Context) ([]model.Device, error) {
	list, err := v.cfg.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		v.fill(&list[i])
	}
	return list, nil
}

// fill 填入运行时值。在线状态没有记录（未握手的占位）按 pending；offline 且允许离线对外显示 dormant。
func (v *Viewer) fill(d *model.Device) {
	state := ""
	if v.cfg.Presence != nil {
		state, _ = v.cfg.Presence.State(d.ID)
	}
	switch {
	case state == "":
		state = model.PresencePending
	case state == model.PresenceOffline && d.AllowOffline:
		state = model.PresenceDormant
	}
	d.Presence = state
	if v.cfg.ConnError != nil {
		d.ConnError = v.cfg.ConnError(d.ID)
	}
	if v.cfg.ClockSkew != nil {
		if skew, ok := v.cfg.ClockSkew(d.ID); ok {
			ms := skew.Milliseconds()
			d.ClockSkewMs = &ms
		}
	}
}
