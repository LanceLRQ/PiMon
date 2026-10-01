package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

// SessionKind 是会话类型。
type SessionKind string

// 会话类型取值。
const (
	KindAdmin  SessionKind = "admin"
	KindScreen SessionKind = "screen"
)

// 会话相关常量，供 HTTP 层设置 Cookie。
const (
	// CookieName 是会话 Cookie 名。
	CookieName = "pimon_session"
	// AdminSessionTTL 是管理员会话的绝对有效期。
	AdminSessionTTL = 30 * 24 * time.Hour
)

// Sessions 管理登录会话，库中只存 token 的 sha256。
type Sessions struct {
	db  *store.DB
	clk clock.Clock

	cbMu     sync.RWMutex
	onRevoke func()
}

// OnRevoke 注册会话被撤销（Delete、DeleteKind）后的回调，用于让已建立的长连接立即复核会话。
// 回调同步调用、必须非阻塞；重复注册会覆盖前一个。过期会话不触发（靠调用方定期复核）。
func (s *Sessions) OnRevoke(f func()) {
	s.cbMu.Lock()
	s.onRevoke = f
	s.cbMu.Unlock()
}

func (s *Sessions) revoked() {
	s.cbMu.RLock()
	f := s.onRevoke
	s.cbMu.RUnlock()
	if f != nil {
		f()
	}
}

// NewSessions 创建会话服务。
func NewSessions(db *store.DB, clk clock.Clock) *Sessions {
	return &Sessions{db: db, clk: clk}
}

// Create 创建会话并返回明文 token。管理员会话 30 天后绝对过期，屏幕会话不过期。
func (s *Sessions) Create(ctx context.Context, kind SessionKind) (string, error) {
	if kind != KindAdmin && kind != KindScreen {
		return "", fmt.Errorf("auth: 非法会话类型 %q", kind)
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := s.clk.Now()
	var expires any // 屏幕会话为 NULL
	if kind == KindAdmin {
		expires = store.FormatTime(now.Add(AdminSessionTTL))
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, kind, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), string(kind), store.FormatTime(now), expires)
	if err != nil {
		return "", err
	}
	return token, nil
}

// Lookup 查询 token 对应的会话类型；不存在或已过期返回 ok=false，过期的会话顺手删除。
func (s *Sessions) Lookup(ctx context.Context, token string) (SessionKind, bool, error) {
	hash := hashToken(token)
	var kind string
	var expires sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT kind, expires_at FROM sessions WHERE token_hash = ?`, hash).Scan(&kind, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if expires.Valid {
		exp, err := store.ParseTime(expires.String)
		if err != nil {
			return "", false, err
		}
		if !s.clk.Now().Before(exp) {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash); err != nil {
				return "", false, err
			}
			return "", false, nil
		}
	}
	return SessionKind(kind), true, nil
}

// Delete 删除 token 对应的会话，不存在时不报错。
func (s *Sessions) Delete(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	if err == nil {
		s.revoked()
	}
	return err
}

// DeleteKind 删除某一类型的全部会话。
func (s *Sessions) DeleteKind(ctx context.Context, kind SessionKind) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE kind = ?`, string(kind))
	if err == nil {
		s.revoked()
	}
	return err
}
