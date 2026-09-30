package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
)

func TestAdmin_生命周期(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	a := NewAdmins(openDB(t), clk)

	if ok, err := a.Exists(ctx); err != nil || ok {
		t.Fatalf("初始不应存在: ok=%v err=%v", ok, err)
	}
	if _, err := a.PasswordHash(ctx); !errors.Is(err, ErrNoAdmin) {
		t.Fatalf("期望 ErrNoAdmin: %v", err)
	}
	if err := a.SetPasswordHash(ctx, "x"); !errors.Is(err, ErrNoAdmin) {
		t.Fatalf("期望 ErrNoAdmin: %v", err)
	}
	if err := a.Create(ctx, "hash1"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.Exists(ctx); !ok {
		t.Fatal("创建后应存在")
	}
	if err := a.Create(ctx, "hash2"); !errors.Is(err, ErrAdminExists) {
		t.Fatalf("期望 ErrAdminExists: %v", err)
	}
	if h, err := a.PasswordHash(ctx); err != nil || h != "hash1" {
		t.Fatalf("hash = %q err=%v", h, err)
	}
	if err := a.SetPasswordHash(ctx, "hash3"); err != nil {
		t.Fatal(err)
	}
	if h, _ := a.PasswordHash(ctx); h != "hash3" {
		t.Fatalf("更新后 hash = %q", h)
	}
}

func countAdminSessions(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE kind = 'admin'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAdmin_ReplacePassword更新哈希并吊销管理员会话(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	db := openDB(t)
	a := NewAdmins(db, clk)
	sessions := NewSessions(db, clk)
	if err := a.Create(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Create(ctx, KindAdmin); err != nil {
		t.Fatal(err)
	}
	screen, err := sessions.Create(ctx, KindScreen)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ReplacePassword(ctx, "new"); err != nil {
		t.Fatal(err)
	}
	if h, _ := a.PasswordHash(ctx); h != "new" {
		t.Fatalf("hash = %q", h)
	}
	if n := countAdminSessions(t, db); n != 0 {
		t.Fatalf("管理员会话应全部删除，剩 %d", n)
	}
	if _, ok, err := sessions.Lookup(ctx, screen); err != nil || !ok {
		t.Fatalf("屏幕会话不应受影响: ok=%v err=%v", ok, err)
	}
}

func TestAdmin_ReplacePassword无管理员返回ErrNoAdmin(t *testing.T) {
	a := NewAdmins(openDB(t), newClock())
	if err := a.ReplacePassword(context.Background(), "x"); !errors.Is(err, ErrNoAdmin) {
		t.Fatalf("期望 ErrNoAdmin: %v", err)
	}
}

func TestAdmin_ReplacePassword删会话失败时整体回滚(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	db := openDB(t)
	a := NewAdmins(db, clk)
	if err := a.Create(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSessions(db, clk).Create(ctx, KindAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER block_del BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.ReplacePassword(ctx, "new"); err == nil {
		t.Fatal("删除会话失败应返回错误")
	}
	if h, _ := a.PasswordHash(ctx); h != "old" {
		t.Fatalf("应回滚为旧哈希，得 %q", h)
	}
	if n := countAdminSessions(t, db); n != 1 {
		t.Fatalf("会话应保留，得 %d", n)
	}
}
