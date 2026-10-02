package app

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/httpx"
)

// kioskGate 实现 ws.KioskAuth：用屏幕令牌校验 kiosk 的 Bearer 握手，
// 失败按与 /screen/auth 相同的限流键计数（kiosk 与浏览器共用同一来源的失败额度）。
type kioskGate struct {
	tokens  *auth.ScreenTokens
	limiter *auth.Limiter
	// mu 串行化「查锁定 → 校验 → 记失败」，保证并发握手无法在一个锁定周期内超过失败上限。
	mu sync.Mutex
}

func (g *kioskGate) Authenticate(r *http.Request, token string) (bool, time.Duration, error) {
	key := httpx.LimitKey(auth.KeyScreen, r)
	g.mu.Lock()
	defer g.mu.Unlock()
	if d := g.limiter.Locked(key); d > 0 {
		return false, d, nil
	}
	ok, err := g.tokens.Verify(r.Context(), token)
	if err != nil {
		return false, 0, err
	}
	if !ok {
		if g.limiter.Fail(key) == 0 {
			return false, g.limiter.Locked(key), nil
		}
		return false, 0, nil
	}
	g.limiter.Success(key)
	return true, 0, nil
}

func (g *kioskGate) Verify(ctx context.Context, token string) (bool, error) {
	return g.tokens.Verify(ctx, token)
}
