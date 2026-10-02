package agent

import (
	"strings"
	"testing"
)

func TestCheckCompat_边界(t *testing.T) {
	cases := []struct {
		name                         string
		hubVer, hubMin, agVer, agMin int
		want                         string
	}{
		{"完全相同", 1, 1, 1, 1, ""},
		{"agent 恰为 hub 最低", 3, 2, 2, 1, ""},
		{"agent 低于 hub 最低一档", 3, 2, 1, 1, ReasonVersionTooOld},
		{"hub 恰为 agent 最低", 2, 1, 3, 2, ""},
		{"hub 低于 agent 最低一档", 1, 1, 3, 2, ReasonVersionTooNew},
		{"两个条件同时不满足以 agent 过旧为先", 1, 3, 1, 2, ReasonVersionTooOld},
	}
	for _, c := range cases {
		if got := CheckCompat(c.hubVer, c.hubMin, c.agVer, c.agMin); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestConstants_版本与帧大小(t *testing.T) {
	if ProtocolVersion != 1 || MinCompatibleVersion != 1 {
		t.Fatal("版本常量应为 1")
	}
	if MaxPreAuthFrame != 4*1024 || MaxFrame != 8*1024*1024 {
		t.Fatal("帧上限常量不对")
	}
}

func TestCredential_推导一致(t *testing.T) {
	c1, err := NewCredential()
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := NewCredential()
	if c1 == c2 {
		t.Fatal("两次凭据不应相同")
	}
	// 32 字节 base64url 无填充 = 43 字符
	if len(c1) != 43 || strings.ContainsAny(c1, "=+/") {
		t.Fatalf("凭据格式不对: %q", c1)
	}
	// "abc" 的 sha256
	if got := CredentialHash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("哈希 = %s", got)
	}
	h := CredentialHash("abc")
	if DeviceID("abc") != h[:16] || DeviceIDFromHash(h) != h[:16] {
		t.Fatal("设备 ID 应为哈希前 16 个字符")
	}
}
