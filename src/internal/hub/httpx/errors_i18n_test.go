package httpx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// declaredCodes 汇总 errors.go 里声明的全部错误码：注释表里的每一行加常量定义，
// 再加 ui 包的协议级错误码（它们同样登记在注释表里）。
func declaredCodes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("errors.go")
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	// 注释表行：`//\t<code>  说明`；表头那一行的 code 列叫 "code"，跳过
	tableRow := regexp.MustCompile(`(?m)^//\t([a-z][a-z_.]*)\s`)
	for _, m := range tableRow.FindAllStringSubmatch(string(src), -1) {
		if m[1] != "code" {
			set[m[1]] = true
		}
	}
	constDef := regexp.MustCompile(`(?m)^\s+Code\w+\s*=\s*"([^"]+)"`)
	for _, m := range constDef.FindAllStringSubmatch(string(src), -1) {
		set[m[1]] = true
	}
	set[ui.ErrSubscribeDenied] = true
	set[ui.ErrBadMessage] = true

	codes := make([]string, 0, len(set))
	for c := range set {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return codes
}

// lookup 按「.」逐级在嵌套 JSON 里取字符串译文；errors.<code> 与 i18next 的取法一致。
func lookup(tree map[string]any, path string) (string, bool) {
	var cur any = tree
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		if cur, ok = m[part]; !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok && s != ""
}

// 防止后端新增错误码后忘记同步前端译文（全局约束：新增 code 同步到前端 i18n）。
func TestEveryErrorCodeHasFrontendTranslation(t *testing.T) {
	codes := declaredCodes(t)
	if len(codes) < 20 {
		t.Fatalf("解析出的错误码过少（%d 个），errors.go 注释表格式可能变了：%v", len(codes), codes)
	}
	for _, lang := range []string{"zh", "en"} {
		path := filepath.Join("..", "..", "..", "..", "web", "src", "i18n", lang+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var tree map[string]any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatalf("%s 不是合法 JSON：%v", path, err)
		}
		for _, code := range codes {
			if _, ok := lookup(tree, "errors."+code); !ok {
				t.Errorf("错误码 %q 在 %s 中缺少译文（应放在 errors.%s）", code, path, code)
			}
		}
	}
}
