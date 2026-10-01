package auth

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

var codeFormat = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){5}$`)

func TestSetupCodes_生成格式与有效(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	s := NewSetupCodes(openDB(t), clk, testBox(t))
	if ok, _, err := s.Active(ctx); err != nil || ok {
		t.Fatalf("初始不应有设置码: ok=%v err=%v", ok, err)
	}
	code, exp, err := s.Generate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !codeFormat.MatchString(code) {
		t.Fatalf("格式不符: %s", code)
	}
	if !exp.Equal(testStart.Add(24 * time.Hour)) {
		t.Fatalf("过期时间 = %v", exp)
	}
	ok, gotExp, err := s.Active(ctx)
	if err != nil || !ok || !gotExp.Equal(exp) {
		t.Fatalf("Active: ok=%v exp=%v err=%v", ok, gotExp, err)
	}
	if ok, err := s.Verify(ctx, code); err != nil || !ok {
		t.Fatalf("应校验通过: ok=%v err=%v", ok, err)
	}
}

func TestSetupCodes_校验容忍大小写分隔符与易混字符(t *testing.T) {
	ctx := context.Background()
	s := NewSetupCodes(openDB(t), newClock(), testBox(t))
	code, _, _ := s.Generate(ctx)
	compact := strings.ReplaceAll(code, "-", "")
	for _, in := range []string{
		strings.ToLower(code),
		compact,
		strings.ReplaceAll(code, "-", " "),
		"  " + code + "\n",
	} {
		if ok, err := s.Verify(ctx, in); err != nil || !ok {
			t.Errorf("%q 应通过: ok=%v err=%v", in, ok, err)
		}
	}
	if ok, _ := s.Verify(ctx, "0000-0000-0000-0000-0000-0000"); ok {
		t.Error("错误码不应通过")
	}
}

func TestNormalizeSetupCode_易混字符映射(t *testing.T) {
	if got := normalizeSetupCode("ab-o i\tl"); got != "AB011" {
		t.Fatalf("规范化结果 = %q, 期望 AB011", got)
	}
}

func TestSetupCodes_过期失效(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	s := NewSetupCodes(openDB(t), clk, testBox(t))
	code, _, _ := s.Generate(ctx)
	clk.Advance(24*time.Hour - time.Second)
	if ok, _ := s.Verify(ctx, code); !ok {
		t.Fatal("过期前应有效")
	}
	clk.Advance(time.Second)
	if ok, err := s.Verify(ctx, code); err != nil || ok {
		t.Fatalf("过期后应无效: ok=%v err=%v", ok, err)
	}
	if ok, _, _ := s.Active(ctx); ok {
		t.Fatal("过期后 Active 应为 false")
	}
}

func TestSetupCodes_消费后失效(t *testing.T) {
	ctx := context.Background()
	s := NewSetupCodes(openDB(t), newClock(), testBox(t))
	code, _, _ := s.Generate(ctx)
	if err := s.Consume(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Verify(ctx, code); ok {
		t.Fatal("消费后应无效")
	}
	if err := s.Consume(ctx); err != nil {
		t.Fatalf("重复消费应幂等: %v", err)
	}
}

func TestSetupCodes_重新生成后旧码失效(t *testing.T) {
	ctx := context.Background()
	s := NewSetupCodes(openDB(t), newClock(), testBox(t))
	old, _, _ := s.Generate(ctx)
	fresh, _, _ := s.Generate(ctx)
	if old == fresh {
		t.Fatal("两次生成不应相同")
	}
	if ok, _ := s.Verify(ctx, old); ok {
		t.Fatal("旧码应失效")
	}
	if ok, _ := s.Verify(ctx, fresh); !ok {
		t.Fatal("新码应有效")
	}
}

func TestSetupCodes_无记录时校验为false(t *testing.T) {
	s := NewSetupCodes(openDB(t), newClock(), testBox(t))
	if ok, err := s.Verify(context.Background(), "whatever"); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestSetupCodes_Reveal返回明文并随码变化(t *testing.T) {
	ctx := context.Background()
	s := NewSetupCodes(openDB(t), newClock(), testBox(t))
	if _, _, ok, err := s.Reveal(ctx); err != nil || ok {
		t.Fatalf("无码时 ok=%v err=%v", ok, err)
	}
	code, exp, _ := s.Generate(ctx)
	got, gotExp, ok, err := s.Reveal(ctx)
	if err != nil || !ok || got != code || !gotExp.Equal(exp) {
		t.Fatalf("Reveal = %q %v ok=%v err=%v, 期望 %q", got, gotExp, ok, err, code)
	}
	fresh, _, _ := s.Generate(ctx)
	if got, _, _, _ := s.Reveal(ctx); got != fresh {
		t.Fatalf("重新生成后应显示新码: %q", got)
	}
}

func TestSetupCodes_库里不存明文(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s := NewSetupCodes(db, newClock(), testBox(t))
	code, _, _ := s.Generate(ctx)
	var enc string
	if err := db.QueryRowContext(ctx, `SELECT code_enc FROM setup_codes WHERE id = 1`).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc == "" || strings.Contains(enc, strings.ReplaceAll(code, "-", "")) || strings.Contains(enc, code) {
		t.Fatalf("code_enc 应为密文: %q", enc)
	}
}

func TestSetupCodes_旧码没有密文时不可显示(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s := NewSetupCodes(db, newClock(), testBox(t))
	if _, _, err := s.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE setup_codes SET code_enc = '' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.Reveal(ctx); err != nil || ok {
		t.Fatalf("旧码应不可显示: ok=%v err=%v", ok, err)
	}
	if ok, _, _ := s.Active(ctx); !ok {
		t.Fatal("旧码仍然有效，只是不可显示")
	}
}

func TestSetupCodes_换密钥后不可显示(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	clk := newClock()
	NewSetupCodes(db, clk, testBox(t)).Generate(ctx) //nolint:errcheck
	other := NewSetupCodes(db, clk, testBox(t))
	if _, _, ok, err := other.Reveal(ctx); err != nil || ok {
		t.Fatalf("密钥不符应按不可显示处理: ok=%v err=%v", ok, err)
	}
}

func TestSetupCodes_消费后整行删除(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	s := NewSetupCodes(db, newClock(), testBox(t))
	_, _, _ = s.Generate(ctx)
	if err := s.Consume(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM setup_codes`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("行数 = %d err=%v", n, err)
	}
}
