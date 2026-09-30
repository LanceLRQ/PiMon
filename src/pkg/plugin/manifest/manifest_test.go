package manifest

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

// glmExample 是设计 2.2 节的示例 manifest。
const glmExample = `id: glm-coding-plan
version: 1.0.0                 # 插件版本
api_version: 1                 # 插件接口版本（exec stdin/stdout 协议版本）
name: {zh: GLM 编程套餐, en: GLM Coding Plan}   # 文本字段均可写成多语言映射，或单一字符串
kind: source                   # source | notifier
runtime: builtin               # builtin | exec
runs_on: [hub, agent]
interval: 300s                 # 默认刷新间隔，实例可覆盖
timeout: 30s                   # 单次运行超时（所有形态）
config_schema:                 # 有序列表，网页按此顺序生成表单
  - {key: account, type: string, title: {zh: 账户名, en: Account}, required: true}
  - {key: api_key, type: secret, title: Coding Plan Key, required: true}
  - {key: proxy,   type: proxy,  title: {zh: 代理, en: Proxy}}
outputs:
  - {key: quota.5h,   type: quota, title: {zh: 5 小时额度, en: 5-hour quota}}
  - {key: quota.tool, type: quota, title: {zh: 工具调用, en: Tool calls}}
widgets:
  - id: quota
    name: {zh: GLM 额度, en: GLM quota}
    sizes:
      1x1: {template: value,       bind: {value: {item: quota.5h, field: remaining_pct}}}
      2x1: {template: quota,       bind: {window: {item: quota.5h}}}
      2x2: {template: quota-multi, bind: {windows: [{item: quota.5h}, {item: quota.tool}]}}
alerts:                        # 默认告警规则，创建实例时生成，可改可关
  - {name: {zh: 额度不足, en: Low quota}, item: quota.5h, field: remaining_pct, op: "<", value: 10, severity: warning}
`

func lineOf(t *testing.T, src, needle string) int {
	t.Helper()
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, needle) {
			return i + 1
		}
	}
	t.Fatalf("未找到 %q", needle)
	return 0
}

func TestParseGLMExample(t *testing.T) {
	m, err := Parse([]byte(glmExample))
	if err != nil {
		t.Fatalf("GLM 示例应解析通过: %v", err)
	}
	if m.ID != "glm-coding-plan" || m.Version != "1.0.0" || m.APIVersion != 1 {
		t.Errorf("基础字段错误: %+v", m)
	}
	if m.Name.Get("zh") != "GLM 编程套餐" || m.Name.Get("en") != "GLM Coding Plan" {
		t.Errorf("name 错误: %+v", m.Name)
	}
	if m.Kind != KindSource || m.Runtime != RuntimeBuiltin {
		t.Errorf("kind/runtime 错误: %v %v", m.Kind, m.Runtime)
	}
	if len(m.RunsOn) != 2 || m.RunsOn[0] != "hub" || m.RunsOn[1] != "agent" {
		t.Errorf("runs_on 错误: %v", m.RunsOn)
	}
	if m.Interval != 300*time.Second || m.Timeout != 30*time.Second {
		t.Errorf("interval/timeout 错误: %v %v", m.Interval, m.Timeout)
	}
	if len(m.ConfigSchema) != 3 || m.ConfigSchema[1].Type != schema.TypeSecret || m.ConfigSchema[1].Title.Get("zh") != "Coding Plan Key" {
		t.Errorf("config_schema 错误: %+v", m.ConfigSchema)
	}
	if len(m.Outputs) != 2 || m.Outputs[0].Key != "quota.5h" || m.Outputs[0].Type != "quota" {
		t.Errorf("outputs 错误: %+v", m.Outputs)
	}
	if len(m.Widgets) != 1 || m.Widgets[0].ID != "quota" || len(m.Widgets[0].Sizes) != 3 {
		t.Fatalf("widgets 错误: %+v", m.Widgets)
	}
	s := m.Widgets[0].Sizes
	if s[0].Size != "1x1" || s[0].Cols != 1 || s[0].Rows != 1 || s[0].Template != "value" || s[2].Size != "2x2" {
		t.Errorf("sizes 顺序或内容错误: %+v", s)
	}
	b := s[0].Bind[0]
	if b.Slot != "value" || b.List || len(b.Refs) != 1 || b.Refs[0].Item != "quota.5h" || b.Refs[0].Field != "remaining_pct" {
		t.Errorf("单值绑定错误: %+v", b)
	}
	w := s[2].Bind[0]
	if w.Slot != "windows" || !w.List || len(w.Refs) != 2 || w.Refs[1].Item != "quota.tool" {
		t.Errorf("列表绑定错误: %+v", w)
	}
	if len(m.Alerts) != 1 || m.Alerts[0].Item != "quota.5h" || m.Alerts[0].Op != "<" || m.Alerts[0].Severity != "warning" {
		t.Errorf("alerts 错误: %+v", m.Alerts)
	}
}

func TestI18nFallbackAndDefaults(t *testing.T) {
	src := `id: min
version: 0.1.0
api_version: 1
name: Just A Name
kind: source
runtime: exec
runs_on: [hub]
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name.Get("zh") != "Just A Name" || m.Name.Get("en") != "Just A Name" {
		t.Errorf("单字符串应两种语言通用: %+v", m.Name)
	}
	if m.Interval != DefaultInterval || m.Timeout != DefaultTimeout {
		t.Errorf("未写 interval/timeout 应取默认值: %v %v", m.Interval, m.Timeout)
	}
	m2, err := Parse([]byte(strings.Replace(src, "name: Just A Name", "name: {zh: 仅中文}", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if m2.Name.Get("en") != "仅中文" {
		t.Errorf("缺失语言应回退: %+v", m2.Name)
	}
}

// 以 GLM 示例为底，替换一段文本制造错误，返回解析结果。
func mutate(t *testing.T, old, repl string) (string, error) {
	t.Helper()
	if !strings.Contains(glmExample, old) {
		t.Fatalf("模板里没有 %q", old)
	}
	src := strings.Replace(glmExample, old, repl, 1)
	_, err := Parse([]byte(src))
	return src, err
}

func problems(t *testing.T, err error) []Problem {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("应返回 *Error，得到 %T %v", err, err)
	}
	return e.Problems
}

func expectProblem(t *testing.T, src string, err error, needle, msg string) {
	t.Helper()
	want := lineOf(t, src, needle)
	for _, p := range problems(t, err) {
		if p.Line == want && strings.Contains(p.Path+" "+p.Message, msg) {
			return
		}
	}
	t.Errorf("未找到第 %d 行含 %q 的问题: %+v", want, msg, problems(t, err))
}

func TestFieldErrorsWithLines(t *testing.T) {
	cases := []struct {
		name, old, repl, needle, msg string
	}{
		{"重复 config key", "  - {key: proxy,   type: proxy,", "  - {key: account, type: proxy,", "key: account, type: proxy", "重复"},
		{"非法字段 type", "type: proxy,  title", "type: nonsense,  title", "type: nonsense", "type"},
		{"bind 引用未声明 item", "bind: {window: {item: quota.5h}}", "bind: {window: {item: quota.nope}}", "quota.nope", "quota.nope"},
		{"尺寸格式错误", "      2x1: {template: quota,", "      2by1: {template: quota,", "2by1", "尺寸"},
		{"尺寸超范围", "      2x1: {template: quota,", "      7x1: {template: quota,", "7x1", "范围"},
		{"未知模板", "template: quota-multi", "template: fancy", "template: fancy", "模板"},
		{"非法 id", "id: glm-coding-plan", "id: GLM_Plan", "id: GLM_Plan", "id"},
		{"非法 kind", "kind: source", "kind: sink", "kind: sink", "kind"},
		{"非法 runtime", "runtime: builtin ", "runtime: http    ", "runtime: http", "runtime"},
		{"interval 无法解析", "interval: 300s ", "interval: soon ", "interval: soon", "interval"},
		{"timeout 无法解析", "timeout: 30s ", "timeout: 0s  ", "timeout: 0s", "timeout"},
		{"runs_on 非法", "runs_on: [hub, agent]", "runs_on: [hub, moon]", "runs_on", "runs_on"},
		{"outputs type 非法", "key: quota.tool, type: quota", "key: quota.tool, type: graph", "type: graph", "type"},
		{"outputs key 重复", "key: quota.tool, type: quota", "key: quota.5h, type: quota", "key: quota.5h, type: quota, title: {zh: 工具", "重复"},
		{"widget 无 sizes", "    sizes:\n", "    sizez:\n", "sizez", "未知"},
		{"alert 引用未声明 item", "item: quota.5h, field: remaining_pct, op", "item: ghost, field: remaining_pct, op", "item: ghost", "ghost"},
		{"alert op 非法", "op: \"<\"", "op: \"~\"", "op: \"~\"", "op"},
		{"未知顶层字段", "kind: source ", "flavour: x\nkind: source ", "flavour", "未知"},
		{"version 格式", "version: 1.0.0 ", "version: one    ", "version: one", "version"},
		{"api_version 不支持", "api_version: 1 ", "api_version: 9 ", "api_version: 9", "api_version"},
	}
	for _, c := range cases {
		src, err := mutate(t, c.old, c.repl)
		if err == nil {
			t.Errorf("%s: 应报错", c.name)
			continue
		}
		expectProblem(t, src, err, c.needle, c.msg)
	}
}

func TestMissingRequired(t *testing.T) {
	_, err := Parse([]byte("id: a-b\n"))
	ps := problems(t, err)
	joined := ""
	for _, p := range ps {
		joined += p.Path + ";"
	}
	for _, k := range []string{"version", "api_version", "name", "kind", "runtime", "runs_on"} {
		if !strings.Contains(joined, k) {
			t.Errorf("应报告缺少 %s: %s", k, joined)
		}
	}
}

func TestDynamicCollectionBind(t *testing.T) {
	src := `id: host
version: 1.0.0
api_version: 1
name: Host
kind: source
runtime: builtin
runs_on: [hub]
outputs:
  - {key: "disk[*]", type: gauge}
  - {key: cpu, type: gauge}
widgets:
  - id: disks
    name: Disks
    sizes:
      2x1: {template: gauge, bind: {value: {item: "disk[/vol1]"}}}
      2x2: {template: list, bind: {rows: [{item: "disk[*]"}, {item: cpu}]}}
`
	if _, err := Parse([]byte(src)); err != nil {
		t.Fatalf("动态集合前缀应可被引用: %v", err)
	}
	bad := strings.Replace(src, `item: "disk[/vol1]"`, `item: "net[eth0]"`, 1)
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("未声明的动态集合应报错")
	}
}

func TestSyntaxErrorHasLine(t *testing.T) {
	_, err := Parse([]byte("id: a\nname: [unclosed\n"))
	ps := problems(t, err)
	if len(ps) == 0 || ps[0].Line == 0 {
		t.Fatalf("语法错误应带行号: %+v", ps)
	}
}

func TestErrorListsAllProblemsInLineOrder(t *testing.T) {
	src := glmExample
	src = strings.Replace(src, "kind: source", "kind: sink", 1)
	src = strings.Replace(src, "id: glm-coding-plan", "id: BAD", 1)
	_, err := Parse([]byte(src))
	ps := problems(t, err)
	if len(ps) < 2 {
		t.Fatalf("应一次列出多条问题: %+v", ps)
	}
	for i := 1; i < len(ps); i++ {
		if ps[i].Line < ps[i-1].Line {
			t.Fatalf("问题应按行号排序: %+v", ps)
		}
	}
	if !strings.Contains(err.Error(), "第 1 行") {
		t.Errorf("Error() 应含行号: %s", err.Error())
	}
}

func TestEmptyAndNonMapping(t *testing.T) {
	for _, src := range []string{"", "- a\n- b\n", "just text"} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%q 应报错", src)
		}
	}
}

func TestVisibleWhenRejectedAtParse(t *testing.T) {
	src := `id: a
version: 1.0.0
api_version: 1
name: A
kind: source
runtime: exec
runs_on: [hub]
config_schema:
  - {key: city, type: lookup}
  - {key: extra, type: string, visible_when: {city: x}}
`
	_, err := Parse([]byte(src))
	want := lineOf(t, src, "key: extra")
	for _, p := range problems(t, err) {
		if p.Line == want && strings.Contains(p.Message, "visible_when") {
			return
		}
	}
	t.Errorf("应在第 %d 行报 visible_when 问题: %v", want, err)
}

func TestOutputTypesFromReport(t *testing.T) {
	if !reflect.DeepEqual(OutputTypes, report.ItemTypes) {
		t.Errorf("OutputTypes 应与 report.ItemTypes 一致: %v", OutputTypes)
	}
}

const fieldCheckBase = `id: fc
version: 1.0.0
api_version: 1
name: FC
kind: source
runtime: builtin
runs_on: [hub]
outputs:
  - {key: q, type: quota}
  - {key: "disk[*]", type: gauge}
  - {key: log, type: table}
  - {key: st, type: state}
widgets:
  - id: w
    name: W
    sizes:
      1x1: {template: value, bind: {value: {item: q, field: remaining_pct}}}
alerts:
  - {name: A, item: q, field: used, op: ">", value: 1}
`

func TestBindAndAlertFieldMustBelongToType(t *testing.T) {
	if _, err := Parse([]byte(fieldCheckBase)); err != nil {
		t.Fatalf("基线应通过: %v", err)
	}
	cases := []struct{ name, old, repl, needle string }{
		{"bind 字段不属于 quota", "field: remaining_pct}}}", "field: value}}}", "field: value"},
		{"alert 字段不属于 quota", "field: used,", "field: value,", "field: value"},
		{"动态成员按前缀类型检查", "item: q, field: remaining_pct}}}", `item: "disk[/vol1]", field: remaining_pct}}}`, "remaining_pct"},
		{"列表写法同样检查", "bind: {value: {item: q, field: remaining_pct}}}", "bind: {value: [{item: st, field: bogus}]}}", "bogus"},
		{"table 不能指定字段之外的名字", "field: remaining_pct}}}", "field: x}}}", "field: x"},
	}
	for _, c := range cases {
		src := strings.Replace(fieldCheckBase, c.old, c.repl, 1)
		if c.name == "table 不能指定字段之外的名字" {
			src = strings.Replace(src, "item: q, field: x", "item: log, field: x", 1)
		}
		_, err := Parse([]byte(src))
		if err == nil {
			t.Errorf("%s: 应报错", c.name)
			continue
		}
		expectProblem(t, src, err, c.needle, "字段")
	}
}

func TestFieldOmittedAndTableAllowed(t *testing.T) {
	src := strings.Replace(fieldCheckBase, "{item: q, field: remaining_pct}", "{item: log}", 1)
	src = strings.Replace(src, "field: used,", "", 1)
	if _, err := Parse([]byte(src)); err != nil {
		t.Fatalf("省略 field（含 table 引用）应合法: %v", err)
	}
	ok := strings.Replace(fieldCheckBase, "field: remaining_pct", "field: used", 1)
	if _, err := Parse([]byte(ok)); err != nil {
		t.Fatalf("类型内字段应合法: %v", err)
	}
}
