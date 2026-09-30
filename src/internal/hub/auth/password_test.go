package auth

import (
	"strings"
	"testing"
)

func TestHasher_哈希后可校验(t *testing.T) {
	h := Hasher{Params: fastParams}
	enc, err := h.Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("编码格式不符: %s", enc)
	}
	ok, err := h.Verify(enc, "correct horse")
	if err != nil || !ok {
		t.Fatalf("应校验通过: ok=%v err=%v", ok, err)
	}
}

func TestHasher_错误密码不通过(t *testing.T) {
	h := Hasher{Params: fastParams}
	enc, _ := h.Hash("correct horse")
	ok, err := h.Verify(enc, "wrong horse")
	if err != nil || ok {
		t.Fatalf("错误密码应返回 false 且无错误: ok=%v err=%v", ok, err)
	}
}

func TestHasher_同密码盐不同(t *testing.T) {
	h := Hasher{Params: fastParams}
	a, _ := h.Hash("same-password")
	b, _ := h.Hash("same-password")
	if a == b {
		t.Fatal("相同密码两次哈希应因盐不同而不同")
	}
}

func TestHasher_参数取自编码而非哈希器(t *testing.T) {
	enc, _ := Hasher{Params: fastParams}.Hash("pw-12345678")
	other := Hasher{Params: Params{Memory: 16, Time: 2, Threads: 2, KeyLen: 32, SaltLen: 16}}
	ok, err := other.Verify(enc, "pw-12345678")
	if err != nil || !ok {
		t.Fatalf("应按编码内参数校验: ok=%v err=%v", ok, err)
	}
}

func TestHasher_格式非法报错(t *testing.T) {
	h := Hasher{Params: fastParams}
	for _, enc := range []string{
		"", "plain", "$argon2i$v=19$m=8,t=1,p=1$AAAA$AAAA",
		"$argon2id$v=18$m=8,t=1,p=1$AAAA$AAAA",
		"$argon2id$v=19$m=x,t=1,p=1$AAAA$AAAA",
		"$argon2id$v=19$m=8,t=1,p=1$!!!$AAAA",
		"$argon2id$v=19$m=8,t=1,p=1$AAAA",
		"$argon2id$v=19$m=8,t=1,p=1$AAAA$",
	} {
		if ok, err := h.Verify(enc, "x"); err == nil || ok {
			t.Errorf("%q 应报错: ok=%v err=%v", enc, ok, err)
		}
	}
}

func TestHasher_默认参数往返(t *testing.T) {
	if MinPasswordLen != 8 {
		t.Fatalf("MinPasswordLen = %d", MinPasswordLen)
	}
	if DefaultParams != (Params{Memory: 64 * 1024, Time: 3, Threads: 2, KeyLen: 32, SaltLen: 16}) {
		t.Fatalf("默认参数不符: %+v", DefaultParams)
	}
	h := Hasher{Params: DefaultParams}
	enc, err := h.Hash("default-params-pw")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(enc, "m=65536,t=3,p=2") {
		t.Fatalf("编码未含默认参数: %s", enc)
	}
	if ok, err := h.Verify(enc, "default-params-pw"); err != nil || !ok {
		t.Fatalf("往返失败: ok=%v err=%v", ok, err)
	}
}
