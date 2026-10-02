package auth

import (
	"errors"
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

func TestHasher_Verify拒绝超限参数(t *testing.T) {
	salt := "AAAAAAAAAAAAAAAAAAAAAA" // 16 字节
	key := strings.Repeat("A", 43)   // 32 字节
	cases := map[string]string{
		"内存超限":    "$argon2id$v=19$m=262145,t=1,p=1$" + salt + "$" + key,
		"轮数超限":    "$argon2id$v=19$m=8,t=17,p=1$" + salt + "$" + key,
		"线程超限":    "$argon2id$v=19$m=8,t=1,p=17$" + salt + "$" + key,
		"盐过短":     "$argon2id$v=19$m=8,t=1,p=1$AAAAAAAAAA$" + key,                       // 7 字节
		"密钥过短":    "$argon2id$v=19$m=8,t=1,p=1$" + salt + "$" + strings.Repeat("A", 20), // 15 字节
		"密钥过长":    "$argon2id$v=19$m=8,t=1,p=1$" + salt + "$" + strings.Repeat("A", 90), // 67 字节
		"线程超过255": "$argon2id$v=19$m=8,t=1,p=300$" + salt + "$" + key,
	}
	for name, enc := range cases {
		if ok, err := (Hasher{}).Verify(enc, "x"); err == nil || ok {
			t.Errorf("%s: 应返回 ErrInvalidHash, ok=%v err=%v", name, ok, err)
		}
	}
}

func TestHasher_Verify接受边界参数(t *testing.T) {
	// 上限值本身合法：m=262144 过重，这里只验证低成本的边界（盐 8 字节、密钥 16 字节）。
	h := Hasher{Params: Params{Memory: 8, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 8}}
	enc, err := h.Hash("pw-12345678")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := h.Verify(enc, "pw-12345678"); err != nil || !ok {
		t.Fatalf("边界参数应通过: ok=%v err=%v", ok, err)
	}
}

func TestHasher_Hash拒绝超限参数(t *testing.T) {
	bad := []Params{
		{Memory: 262145, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16},
		{Memory: 8, Time: 17, Threads: 1, KeyLen: 32, SaltLen: 16},
		{Memory: 8, Time: 1, Threads: 17, KeyLen: 32, SaltLen: 16},
		{Memory: 8, Time: 1, Threads: 1, KeyLen: 15, SaltLen: 16},
		{Memory: 8, Time: 1, Threads: 1, KeyLen: 65, SaltLen: 16},
		{Memory: 8, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 7},
	}
	for _, p := range bad {
		if _, err := (Hasher{Params: p}).Hash("pw-12345678"); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("%+v: 应返回 ErrInvalidHash, 得 %v", p, err)
		}
	}
}
