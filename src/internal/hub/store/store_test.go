package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func tableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrateCreatesTables(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, name := range []string{"settings", "admin", "sessions", "setup_codes", "screen_tokens", "meta", "schema_migrations", "proxies", "layout_versions", "schedule", "screen_ops"} {
		if !tableExists(t, db, name) {
			t.Errorf("表 %s 不存在", name)
		}
	}
}

func TestMigrateIdempotent(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("第 %d 次 Migrate: %v", i+1, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("schema_migrations 行数 = %d，期望 3", n)
	}
}

func TestPragmas(t *testing.T) {
	db := openTemp(t)
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q", mode)
	}
	var fk int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d", fk)
	}
	var bt int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&bt); err != nil {
		t.Fatal(err)
	}
	if bt != 5000 {
		t.Errorf("busy_timeout = %d", bt)
	}
}

func TestAppVersion(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	// meta 表尚不存在：视为全新库
	v, err := db.AppVersion(ctx)
	if err != nil || v != "" {
		t.Fatalf("迁移前 AppVersion = %q, %v", v, err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	v, err = db.AppVersion(ctx)
	if err != nil || v != "" {
		t.Fatalf("迁移后未设置 AppVersion = %q, %v", v, err)
	}
	if err := db.SetAppVersion(ctx, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAppVersion(ctx, "1.2.4"); err != nil {
		t.Fatal(err)
	}
	v, err = db.AppVersion(ctx)
	if err != nil || v != "1.2.4" {
		t.Fatalf("AppVersion = %q, %v", v, err)
	}
}

func TestTimeRoundTrip(t *testing.T) {
	loc := time.FixedZone("x", 8*3600)
	in := time.Date(2026, 9, 30, 12, 34, 56, 123456789, loc)
	s := FormatTime(in)
	if s != "2026-09-30T04:34:56.123456789Z" {
		t.Fatalf("FormatTime = %q", s)
	}
	out, err := ParseTime(s)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Equal(in) || out.Location() != time.UTC {
		t.Errorf("往返不一致: %v vs %v", out, in)
	}
	if _, err := ParseTime("bad"); err == nil {
		t.Error("非法时间应报错")
	}
}

func TestMigrateFSOrderAndVersions(t *testing.T) {
	db := openTemp(t)
	fsys := fstest.MapFS{
		"0002_b.sql": {Data: []byte(`INSERT INTO t(v) VALUES ('b');`)},
		"0001_a.sql": {Data: []byte(`CREATE TABLE t(v TEXT); INSERT INTO t(v) VALUES ('a');`)},
		"0010_c.sql": {Data: []byte(`INSERT INTO t(v) VALUES ('c');`)},
	}
	if err := db.MigrateFS(context.Background(), fsys); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT v FROM t ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got += v
	}
	if got != "abc" {
		t.Errorf("执行顺序 = %q，期望 abc", got)
	}
}

func TestMigrateFSInvalidName(t *testing.T) {
	for _, name := range []string{"init.sql", "1_init.sql", "0001_init.txt", "0001.sql", "0001_Init.sql"} {
		db := openTemp(t)
		fsys := fstest.MapFS{name: {Data: []byte(`SELECT 1;`)}}
		if err := db.MigrateFS(context.Background(), fsys); err == nil {
			t.Errorf("文件名 %q 应报错", name)
		}
	}
}

func TestMigrateFSDuplicateNumber(t *testing.T) {
	db := openTemp(t)
	fsys := fstest.MapFS{
		"0001_a.sql": {Data: []byte(`SELECT 1;`)},
		"0001_b.sql": {Data: []byte(`SELECT 1;`)},
	}
	if err := db.MigrateFS(context.Background(), fsys); err == nil {
		t.Error("重复编号应报错")
	}
}

func TestMigrateFSRollbackOnFailure(t *testing.T) {
	db := openTemp(t)
	fsys := fstest.MapFS{
		"0001_a.sql": {Data: []byte(`CREATE TABLE ok(x INTEGER);`)},
		"0002_b.sql": {Data: []byte(`CREATE TABLE half(x INTEGER); THIS IS NOT SQL;`)},
	}
	if err := db.MigrateFS(context.Background(), fsys); err == nil {
		t.Fatal("坏 SQL 应报错")
	}
	if !tableExists(t, db, "ok") {
		t.Error("0001 已提交，表 ok 应存在")
	}
	if tableExists(t, db, "half") {
		t.Error("0002 应整体回滚，表 half 不应存在")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 2`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("失败的迁移不应记录版本")
	}
}

func TestMigrateFSRejectsNewerDatabase(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	newer := fstest.MapFS{
		"0001_a.sql": {Data: []byte(`CREATE TABLE t(v TEXT);`)},
		"0002_b.sql": {Data: []byte(`INSERT INTO t(v) VALUES ('b');`)},
	}
	if err := db.MigrateFS(ctx, newer); err != nil {
		t.Fatal(err)
	}
	older := fstest.MapFS{"0001_a.sql": {Data: []byte(`CREATE TABLE t(v TEXT);`)}}
	if err := db.MigrateFS(ctx, older); !errors.Is(err, ErrDatabaseNewer) {
		t.Fatalf("err = %v，期望 ErrDatabaseNewer", err)
	}
}

func TestOpenSetsFileMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pimon.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("权限 = %o，期望 600", got)
	}
}

func TestVersionsExported(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if _, ok, err := db.AppliedVersion(ctx); err != nil || ok {
		t.Fatalf("空库应无已应用版本: ok=%v err=%v", ok, err)
	}
	emb, err := EmbeddedVersion()
	if err != nil || emb < 3 {
		t.Fatalf("内嵌最大版本 = %d, err = %v", emb, err)
	}
	if err := db.CheckNotNewer(ctx); err != nil {
		t.Fatalf("空库不应判为更新: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := db.AppliedVersion(ctx); err != nil || !ok || v != emb {
		t.Fatalf("已应用版本 = %d ok=%v err=%v, 期望 %d", v, ok, err, emb)
	}
	if err := db.CheckNotNewer(ctx); err != nil {
		t.Fatalf("同版本不应判为更新: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, 'future')`, emb+1); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckNotNewer(ctx); !errors.Is(err, ErrDatabaseNewer) {
		t.Fatalf("err = %v，期望 ErrDatabaseNewer", err)
	}
}
