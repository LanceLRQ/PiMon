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
	s := NewSetupCodes(openDB(t), clk)
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
	s := NewSetupCodes(openDB(t), newClock())
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
	s := NewSetupCodes(openDB(t), clk)
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
	s := NewSetupCodes(openDB(t), newClock())
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
	s := NewSetupCodes(openDB(t), newClock())
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
	s := NewSetupCodes(openDB(t), newClock())
	if ok, err := s.Verify(context.Background(), "whatever"); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
