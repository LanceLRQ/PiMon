package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.db")
	s, err := Open(path, WithClock(clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestOpen迁移与权限(t *testing.T) {
	s, path := open(t)
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info.Mode())
	}
	if _, err := s.db.Exec(`SELECT COUNT(*) FROM instance_state`); err != nil {
		t.Fatal(err)
	}
	// 重复打开幂等；不预建事件表
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	var tables int
	_ = s2.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables)
	if tables != 2 { // schema_migrations + instance_state
		t.Fatalf("表数量 %d", tables)
	}
}

func TestState读写(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	if _, err := s.State(ctx, "i1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("不存在应返回 sql.ErrNoRows: %v", err)
	}
	if err := s.PutState(ctx, "i1", "p", "c1", `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	got, err := s.State(ctx, "i1")
	if err != nil || got != `{"a":1}` {
		t.Fatalf("got %q err %v", got, err)
	}
	if err := s.DeleteState(ctx, "i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.State(ctx, "i1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err=%v", err)
	}
}

func TestPutState相同内容不写(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	if err := s.PutState(ctx, "i1", "p", "c1", "v"); err != nil {
		t.Fatal(err)
	}
	// 把 updated_at 改成哨兵值：内容相同的 PutState 不应更新它
	if _, err := s.db.Exec(`UPDATE instance_state SET updated_at='sentinel'`); err != nil {
		t.Fatal(err)
	}
	if err := s.PutState(ctx, "i1", "p", "c1", "v"); err != nil {
		t.Fatal(err)
	}
	var ua string
	_ = s.db.QueryRow(`SELECT updated_at FROM instance_state`).Scan(&ua)
	if ua != "sentinel" {
		t.Fatalf("相同内容写了库: %q", ua)
	}
	if err := s.PutState(ctx, "i1", "p", "c1", "v2"); err != nil {
		t.Fatal(err)
	}
	_ = s.db.QueryRow(`SELECT updated_at FROM instance_state`).Scan(&ua)
	if ua == "sentinel" {
		t.Fatal("内容变化应更新")
	}
}

func TestTouch与GC(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	for _, id := range []string{"old", "kept", "fresh"} {
		if err := s.PutState(ctx, id, "p", "", "v"); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.db.Exec(`UPDATE instance_state SET last_assigned_at=?`, FormatForTest(t0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(ctx, []string{"kept", "nope"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(ctx, nil); err != nil {
		t.Fatal(err)
	}
	// kept 被 touch 到假时钟的 2026-06-01；GC 删除 last_assigned_at 早于 2026-03-01 的
	n, err := s.GC(ctx, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("删除 %d 条", n)
	}
	if _, err := s.State(ctx, "kept"); err != nil {
		t.Fatalf("kept 不应被删: %v", err)
	}
	if _, err := s.State(ctx, "old"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old 应被删: %v", err)
	}
}

func TestPutState写入内容摘要且摘要变化也更新(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	if err := s.PutState(ctx, "i1", "p", "c1", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutState(ctx, "i1", "p", "c2", "v"); err != nil {
		t.Fatal(err)
	}
	var ch string
	_ = s.db.QueryRow(`SELECT content_hash FROM instance_state WHERE instance_id='i1'`).Scan(&ch)
	if ch != "c2" {
		t.Fatalf("content_hash = %q", ch)
	}
}

func TestDeleteStateIfContentChanged(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	for id, c := range map[string]string{"same": "c1", "diff": "c1", "unknown": ""} {
		if err := s.PutState(ctx, id, "p", c, "v"); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		id, content string
		deleted     bool
	}{
		{"same", "c1", false},
		{"diff", "c2", true},
		{"unknown", "c2", false}, // 库中摘要未知：不删
		{"same", "", false},      // 下发摘要未知：不删
		{"nope", "c1", false},
	}
	for _, c := range cases {
		got, err := s.DeleteStateIfContentChanged(ctx, c.id, c.content)
		if err != nil || got != c.deleted {
			t.Errorf("%s/%s: deleted=%v err=%v，期望 %v", c.id, c.content, got, err, c.deleted)
		}
	}
	if _, err := s.State(ctx, "diff"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("diff 应被删除")
	}
	if _, err := s.State(ctx, "same"); err != nil {
		t.Fatal("same 不应被删除")
	}
}
