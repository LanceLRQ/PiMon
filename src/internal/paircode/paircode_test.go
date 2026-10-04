package paircode

import (
	"regexp"
	"testing"
)

func TestNew生成8组4位Crockford且每次不同(t *testing.T) {
	re := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){7}$`)
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString(a) || !re.MatchString(b) {
		t.Fatalf("配对码格式不符: %q %q", a, b)
	}
	if a == b {
		t.Fatal("两次生成的配对码相同")
	}
}

func TestHash易混字符与分隔符归一(t *testing.T) {
	if Hash("o1il-0ABC") != Hash("0111 0abc") {
		t.Fatal("O→0、I/L→1 与分隔符归一失败")
	}
	code, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if Hash(code) == Hash(code+"0") {
		t.Fatal("不同配对码哈希相同")
	}
	if len(Hash(code)) != 64 {
		t.Fatalf("哈希应为 sha256 十六进制，得到 %q", Hash(code))
	}
}
