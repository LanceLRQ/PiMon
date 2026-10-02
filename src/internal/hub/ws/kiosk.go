package ws

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// kindKiosk 是 kiosk 连接的内部类型：它不是会话（不落 sessions 表，auth.SessionKind 不含此值），
// 只在连接上用来区分角色。
const kindKiosk auth.SessionKind = "kiosk"

// KioskAuth 校验 kiosk 的 Bearer 屏幕令牌，由 app 层用屏幕令牌服务与限流器实现。
type KioskAuth interface {
	// Authenticate 校验握手令牌并按来源限流计数；locked 大于 0 表示来源被锁定，此时 ok 无意义。
	Authenticate(r *http.Request, token string) (ok bool, locked time.Duration, err error)
	// Verify 复核已建立连接所用的令牌；令牌被轮换后返回 false。
	Verify(ctx context.Context, token string) (bool, error)
}

// KioskSink 接收 kiosk 的上报与唤醒请求，由 screenstate.Service 实现。
// SetKioskOnline 会在持有广播锁时被调用，必须非阻塞且不得回调 Hub；
// Wake 在连接读循环里调用，会触发屏幕状态变化通知。
type KioskSink interface {
	ReportKiosk(r model.KioskReport)
	SetKioskOnline(online bool)
	Wake(ctx context.Context, minutes int) error
}

// kioskJoinedLocked 与 kioskLeftLocked 维护 kiosk 连接数，在线状态切换时通知 KioskSink。须持有 bmu。
func (h *Hub) kioskJoinedLocked() {
	h.kioskConns++
	if h.kioskConns == 1 && h.cfg.Kiosk != nil {
		h.cfg.Kiosk.SetKioskOnline(true)
	}
}

func (h *Hub) kioskLeftLocked() {
	h.kioskConns--
	if h.kioskConns == 0 && h.cfg.Kiosk != nil {
		h.cfg.Kiosk.SetKioskOnline(false)
	}
}

// recheckKiosk 重新校验 kiosk 的令牌：令牌被轮换即以 policy violation 断开；校验出错时保持连接，下次再查。
func (h *Hub) recheckKiosk(c *client) {
	if h.cfg.KioskAuth == nil {
		return
	}
	ok, err := h.cfg.KioskAuth.Verify(c.ctx, c.token)
	if err != nil {
		slog.Warn("复核 kiosk 令牌失败", "err", err)
		return
	}
	if !ok {
		c.kill(websocket.StatusPolicyViolation, "session revoked", false)
	}
}

// handleKiosk 处理 kiosk_report 与 kiosk_wake：只有 kiosk 连接可用，其余连接收到协议错误。
func (h *Hub) handleKiosk(c *client, msg ui.ClientMessage) {
	if c.kind != kindKiosk || h.cfg.Kiosk == nil {
		c.sendError(ui.ErrBadMessage, map[string]any{})
		return
	}
	switch msg.Type {
	case ui.TypeKioskReport:
		if msg.Kiosk == nil {
			c.sendError(ui.ErrBadMessage, map[string]any{})
			return
		}
		h.cfg.Kiosk.ReportKiosk(*msg.Kiosk)
	case ui.TypeKioskWake:
		if err := h.cfg.Kiosk.Wake(c.ctx, msg.Minutes); err != nil {
			var fe model.FieldErrors
			if errors.As(err, &fe) {
				c.sendError(ui.ErrBadMessage, map[string]any{"minutes": fe["minutes"]})
				return
			}
			slog.Warn("kiosk 请求唤醒失败", "err", err)
		}
	}
}
