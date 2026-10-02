package auth

import (
	"context"
	"testing"
	"time"
)

func TestSessions_创建与查询(t *testing.T) {
	ctx := context.Background()
	s := NewSessions(openDB(t), newClock())
	tok, err := s.Create(ctx, KindAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 43 {
		t.Fatalf("token 长度 = %d, 期望 43 (32 字节 base64url)", len(tok))
	}
	kind, ok, err := s.Lookup(ctx, tok)
	if err != nil || !ok || kind != KindAdmin {
		t.Fatalf("kind=%v ok=%v err=%v", kind, ok, err)
	}
	if _, ok, _ := s.Lookup(ctx, "unknown"); ok {
		t.Fatal("未知 token 不应命中")
	}
}

func TestSessions_库内只存哈希(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s := NewSessions(db, newClock())
	tok, _ := s.Create(ctx, KindAdmin)
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT token_hash FROM sessions`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == tok || len(stored) != 64 {
		t.Fatalf("库内应为 sha256 十六进制: %q", stored)
	}
}

func TestSessions_管理员会话30天绝对过期(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	clk := newClock()
	s := NewSessions(db, clk)
	tok, _ := s.Create(ctx, KindAdmin)
	clk.Advance(AdminSessionTTL - time.Second)
	if _, ok, _ := s.Lookup(ctx, tok); !ok {
		t.Fatal("过期前应有效")
	}
	clk.Advance(time.Second)
	if _, ok, err := s.Lookup(ctx, tok); err != nil || ok {
		t.Fatalf("过期后应无效: ok=%v err=%v", ok, err)
	}
	var n int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Fatalf("过期会话应被顺手删除, 剩 %d", n)
	}
	if AdminSessionTTL != 30*24*time.Hour {
		t.Fatalf("AdminSessionTTL = %v", AdminSessionTTL)
	}
}

func TestSessions_屏幕会话不过期(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	clk := newClock()
	s := NewSessions(db, clk)
	tok, _ := s.Create(ctx, KindScreen)
	var exp *string
	_ = db.QueryRowContext(ctx, `SELECT expires_at FROM sessions`).Scan(&exp)
	if exp != nil {
		t.Fatalf("屏幕会话 expires_at 应为 NULL: %v", *exp)
	}
	clk.Advance(10 * 365 * 24 * time.Hour)
	kind, ok, err := s.Lookup(ctx, tok)
	if err != nil || !ok || kind != KindScreen {
		t.Fatalf("kind=%v ok=%v err=%v", kind, ok, err)
	}
}

func TestSessions_Delete与DeleteKind(t *testing.T) {
	ctx := context.Background()
	s := NewSessions(openDB(t), newClock())
	a1, _ := s.Create(ctx, KindAdmin)
	a2, _ := s.Create(ctx, KindAdmin)
	s1, _ := s.Create(ctx, KindScreen)

	if err := s.Delete(ctx, a1); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Lookup(ctx, a1); ok {
		t.Fatal("a1 应已删除")
	}
	if err := s.DeleteKind(ctx, KindScreen); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Lookup(ctx, s1); ok {
		t.Fatal("屏幕会话应已删除")
	}
	if _, ok, _ := s.Lookup(ctx, a2); !ok {
		t.Fatal("DeleteKind 不应影响管理员会话")
	}
	if err := s.Delete(ctx, "not-exist"); err != nil {
		t.Fatalf("删除不存在的会话应幂等: %v", err)
	}
}

func TestSessions_非法kind被拒绝(t *testing.T) {
	if _, err := NewSessions(openDB(t), newClock()).Create(context.Background(), SessionKind("root")); err == nil {
		t.Fatal("非法 kind 应报错")
	}
}

func TestSessions_PruneKind只保留最新N条且不动其他类型(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	s := NewSessions(openDB(t), clk)
	admin, _ := s.Create(ctx, KindAdmin)
	var screens []string
	for i := 0; i < 5; i++ {
		clk.Advance(time.Second)
		tok, _ := s.Create(ctx, KindScreen)
		screens = append(screens, tok)
	}
	revoked := 0
	s.OnRevoke(func() { revoked++ })
	if err := s.PruneKind(ctx, KindScreen, 3); err != nil {
		t.Fatal(err)
	}
	for i, tok := range screens {
		_, ok, _ := s.Lookup(ctx, tok)
		if want := i >= 2; ok != want {
			t.Fatalf("第 %d 条 ok=%v 期望 %v", i, ok, want)
		}
	}
	if _, ok, _ := s.Lookup(ctx, admin); !ok {
		t.Fatal("管理员会话不应被删")
	}
	if revoked != 1 {
		t.Fatalf("删除了会话应触发一次撤销回调, 得到 %d", revoked)
	}
	if err := s.PruneKind(ctx, KindScreen, 3); err != nil || revoked != 1 {
		t.Fatalf("没有删除时不应触发回调: err=%v revoked=%d", err, revoked)
	}
}
