package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// ErrDatabaseNewer 表示数据库已应用的迁移版本高于当前程序所含的最大版本。
var ErrDatabaseNewer = errors.New("数据库来自更新的版本，不支持降级，请用升级前备份恢复")

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version int
	name    string
}

// Migrate 执行内嵌的迁移文件。
func (d *DB) Migrate(ctx context.Context) error {
	sub, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		return fmt.Errorf("读取内嵌迁移: %w", err)
	}
	return d.MigrateFS(ctx, sub)
}

// MigrateFS 按编号顺序执行 fsys 根目录下形如 NNNN_名称.sql 的迁移文件。
// 每个文件在独立事务中执行，失败则整个文件回滚；已记录在 schema_migrations 的版本会跳过。
// 只向前迁移，不支持回退。
func (d *DB) MigrateFS(ctx context.Context, fsys fs.FS) error {
	migrations, err := listMigrations(fsys)
	if err != nil {
		return err
	}
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name    TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("创建 schema_migrations: %w", err)
	}
	var applied sql.NullInt64
	if err := d.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&applied); err != nil {
		return fmt.Errorf("读取已应用迁移版本: %w", err)
	}
	if applied.Valid && (len(migrations) == 0 || int(applied.Int64) > migrations[len(migrations)-1].version) {
		return fmt.Errorf("%w（数据库版本 %d）", ErrDatabaseNewer, applied.Int64)
	}
	for _, m := range migrations {
		if err := d.applyMigration(ctx, fsys, m); err != nil {
			return err
		}
	}
	return nil
}

func listMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("读取迁移目录: %w", err)
	}
	var list []migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		sub := migrationName.FindStringSubmatch(e.Name())
		if sub == nil {
			return nil, fmt.Errorf("迁移文件名不合法: %q（应为 NNNN_名称.sql）", e.Name())
		}
		v, _ := strconv.Atoi(sub[1])
		if prev, ok := seen[v]; ok {
			return nil, fmt.Errorf("迁移编号 %04d 重复: %q 与 %q", v, prev, e.Name())
		}
		seen[v] = e.Name()
		list = append(list, migration{version: v, name: e.Name()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	return list, nil
}

func (d *DB) applyMigration(ctx context.Context, fsys fs.FS, m migration) error {
	var n int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&n); err != nil {
		return fmt.Errorf("查询迁移 %s: %w", m.name, err)
	}
	if n > 0 {
		return nil
	}
	body, err := fs.ReadFile(fsys, m.name)
	if err != nil {
		return fmt.Errorf("读取迁移 %s: %w", m.name, err)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启迁移事务 %s: %w", m.name, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("执行迁移 %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`,
		m.version, m.name); err != nil {
		return fmt.Errorf("记录迁移 %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交迁移 %s: %w", m.name, err)
	}
	return nil
}
