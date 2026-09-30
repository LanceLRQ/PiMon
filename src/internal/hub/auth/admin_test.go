package auth

import (
	"context"
	"errors"
	"testing"
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
