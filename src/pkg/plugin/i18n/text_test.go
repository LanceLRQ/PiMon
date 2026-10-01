package i18n

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func node(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func TestGetFallback(t *testing.T) {
	cases := []struct {
		name string
		text Text
		lang string
		want string
	}{
		{"zh 命中", Text{ZH: "账户", EN: "Account"}, "zh", "账户"},
		{"en 命中", Text{ZH: "账户", EN: "Account"}, "en", "Account"},
		{"en 缺失回退 zh", Text{ZH: "账户"}, "en", "账户"},
		{"zh 缺失回退 en", Text{EN: "Account"}, "zh", "Account"},
		{"未知语言取 en", Text{ZH: "账户", EN: "Account"}, "fr", "Account"},
		{"全空", Text{}, "zh", ""},
	}
	for _, c := range cases {
		if got := c.text.Get(c.lang); got != c.want {
			t.Errorf("%s: 得到 %q，期望 %q", c.name, got, c.want)
		}
	}
}

func TestDecode(t *testing.T) {
	got, msg := Decode(node(t, "Coding Plan Key"))
	if msg != "" || got.ZH != "Coding Plan Key" || got.EN != "Coding Plan Key" {
		t.Fatalf("标量应同时填充两种语言: %+v %q", got, msg)
	}
	got, msg = Decode(node(t, "{zh: 账户名, en: Account}"))
	if msg != "" || got.ZH != "账户名" || got.EN != "Account" {
		t.Fatalf("映射解析错误: %+v %q", got, msg)
	}
	got, msg = Decode(node(t, "{zh: 仅中文}"))
	if msg != "" || got.Get("en") != "仅中文" {
		t.Fatalf("缺失语言应回退: %+v %q", got, msg)
	}
	if _, msg = Decode(node(t, "{fr: x}")); msg == "" {
		t.Fatal("未知语言键应报错")
	}
	if _, msg = Decode(node(t, "{}")); msg == "" {
		t.Fatal("空映射应报错")
	}
	if _, msg = Decode(node(t, "[a]")); msg == "" {
		t.Fatal("序列应报错")
	}
}
