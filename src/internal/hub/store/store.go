// Package store 封装 SQLite 存储：打开数据库、内嵌 SQL 迁移、meta 表与时间格式助手。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动
)

const metaKeyAppVersion = "app_version"

// DB 嵌入 *sql.DB，附带迁移与 meta 访问方法。
type DB struct {
	*sql.DB
}

// Open 打开（必要时创建）path 指向的 SQLite 文件。
// 使用 WAL、5 秒 busy_timeout、外键约束与 NORMAL 同步级别；
// 读写量很小，限制为单连接以避免 SQLITE_BUSY。
func Open(path string) (*DB, error) {
	q := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"foreign_keys(1)",
		"synchronous(NORMAL)",
	} {
		q.Add("_pragma", p)
	}
	dsn := (&url.URL{Scheme: "file", Opaque: url.PathEscape(path), RawQuery: q.Encode()}).String()

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	return &DB{DB: sqlDB}, nil
}

// AppVersion 返回 meta 中记录的应用版本；meta 表尚不存在或未设置时返回空串（全新库）。
func (d *DB) AppVersion(ctx context.Context) (string, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&n)
	if err != nil {
		return "", fmt.Errorf("检查 meta 表: %w", err)
	}
	if n == 0 {
		return "", nil
	}
	var v string
	err = d.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, metaKeyAppVersion).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取应用版本: %w", err)
	}
	return v, nil
}

// SetAppVersion 记录当前应用版本。
func (d *DB) SetAppVersion(ctx context.Context, v string) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		metaKeyAppVersion, v)
	if err != nil {
		return fmt.Errorf("写入应用版本: %w", err)
	}
	return nil
}
