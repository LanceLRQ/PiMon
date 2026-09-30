package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

var (
	// ErrAdminExists 表示管理员已存在，不能重复创建。
	ErrAdminExists = errors.New("auth: 管理员已存在")
	// ErrNoAdmin 表示管理员尚未创建。
	ErrNoAdmin = errors.New("auth: 管理员不存在")
)

// Admins 管理单管理员记录（admin 表仅一行）。
type Admins struct {
	db  *store.DB
	clk clock.Clock
}

// NewAdmins 创建管理员服务。
func NewAdmins(db *store.DB, clk clock.Clock) *Admins {
	return &Admins{db: db, clk: clk}
}

// Exists 返回管理员是否已创建。
func (a *Admins) Exists(ctx context.Context) (bool, error) {
	var n int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin WHERE id = 1`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// Create 创建管理员；已存在时返回 ErrAdminExists。
func (a *Admins) Create(ctx context.Context, passwordHash string) error {
	now := store.FormatTime(a.clk.Now())
	res, err := a.db.ExecContext(ctx,
		`INSERT INTO admin (id, password_hash, created_at, updated_at) VALUES (1, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		passwordHash, now, now)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrAdminExists
	}
	return nil
}

// PasswordHash 返回管理员密码哈希；无管理员时返回 ErrNoAdmin。
func (a *Admins) PasswordHash(ctx context.Context) (string, error) {
	var h string
	err := a.db.QueryRowContext(ctx, `SELECT password_hash FROM admin WHERE id = 1`).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoAdmin
	}
	return h, err
}

// SetPasswordHash 更新管理员密码哈希；无管理员时返回 ErrNoAdmin。
func (a *Admins) SetPasswordHash(ctx context.Context, passwordHash string) error {
	res, err := a.db.ExecContext(ctx,
		`UPDATE admin SET password_hash = ?, updated_at = ? WHERE id = 1`,
		passwordHash, store.FormatTime(a.clk.Now()))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoAdmin
	}
	return nil
}
