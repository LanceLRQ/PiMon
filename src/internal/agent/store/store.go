// Package store 是 agent 的本地 SQLite（agent.db）：保存插件私有 state，嵌入自己的迁移。
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"time"

	hubstore "github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// Store 封装 agent.db 的访问。
type Store struct {
	db  *hubstore.DB
	clk clock.Clock
}

// Option 调整 Store 的可注入依赖。
type Option func(*Store)

// WithClock 注入时钟，默认真实时钟。
func WithClock(c clock.Clock) Option { return func(s *Store) { s.clk = c } }

// Open 打开（必要时创建）path 指向的库并执行内嵌迁移；文件权限 0600。
func Open(path string, opts ...Option) (*Store, error) {
	db, err := hubstore.Open(path)
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("读取内嵌迁移: %w", err)
	}
	if err := db.MigrateFS(context.Background(), sub); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("迁移 agent 库: %w", err)
	}
	s := &Store{db: db, clk: clock.Real{}}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) now() string { return hubstore.FormatTime(s.clk.Now()) }

// State 返回实例的私有 state；不存在时返回 sql.ErrNoRows。
func (s *Store) State(ctx context.Context, id string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT state FROM instance_state WHERE instance_id = ?`, id).Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", err
		}
		return "", fmt.Errorf("读取 state: %w", err)
	}
	return v, nil
}

// PutState 写入实例的私有 state；内容与插件归属都未变化时不写库。
// 新建行时 last_assigned_at 一并设为当前时间。
func (s *Store) PutState(ctx context.Context, id, pluginID, state string) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO instance_state (instance_id, plugin_id, state, updated_at, last_assigned_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(instance_id) DO UPDATE SET
    plugin_id = excluded.plugin_id, state = excluded.state, updated_at = excluded.updated_at
WHERE instance_state.state <> excluded.state OR instance_state.plugin_id <> excluded.plugin_id`,
		id, pluginID, state, now, now)
	if err != nil {
		return fmt.Errorf("写入 state: %w", err)
	}
	return nil
}

// Touch 把仍被分配的实例的 last_assigned_at 刷新为当前时间；不存在的 id 忽略。
func (s *Store) Touch(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("刷新分配时间: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE instance_state SET last_assigned_at = ? WHERE instance_id = ?`, now, id); err != nil {
			return fmt.Errorf("刷新分配时间: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteState 删除实例的 state；不存在时不报错。
func (s *Store) DeleteState(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM instance_state WHERE instance_id = ?`, id); err != nil {
		return fmt.Errorf("删除 state: %w", err)
	}
	return nil
}

// GC 删除最后一次被分配时间早于 before 的 state，返回删除条数。
func (s *Store) GC(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM instance_state WHERE last_assigned_at < ?`, hubstore.FormatTime(before))
	if err != nil {
		return 0, fmt.Errorf("清理 state: %w", err)
	}
	return res.RowsAffected()
}
