package plugintest

import (
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// recorder 捕获失败信息而不让外层测试失败。
type recorder struct {
	testing.TB
	msgs []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(f string, a ...any) {
	r.msgs = append(r.msgs, strings.TrimSpace(f))
}
func (r *recorder) Fatalf(f string, a ...any) { r.Errorf(f, a...) }
func (r *recorder) Fatal(a ...any)            { r.msgs = append(r.msgs, "fatal") }

const goodYAML = `
id: demo
version: 1.0.0
api_version: 1
name: {zh: 演示, en: Demo}
kind: source
runtime: builtin
runs_on: [hub]
outputs:
  - {key: "box[*]", type: state, title: {zh: 盒, en: Box}}
widgets:
  - id: w
    name: {zh: 盒, en: Box}
    sizes:
      2x2: {template: status-grid, bind: {items: {item: "box[*]"}}}
`

func mustParse(t *testing.T, y string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(y))
	if err != nil {
		t.Fatalf("解析 manifest: %v", err)
	}
	return m
}

func TestAssertWidgetSizesAcceptsSupported(t *testing.T) {
	r := &recorder{TB: t}
	AssertWidgetSizes(r, mustParse(t, goodYAML))
	if len(r.msgs) != 0 {
		t.Fatalf("支持的尺寸不应失败: %v", r.msgs)
	}
}

func TestAssertWidgetSizesRejectsUnsupported(t *testing.T) {
	r := &recorder{TB: t}
	AssertWidgetSizes(r, mustParse(t, strings.Replace(goodYAML, "2x2:", "1x1:", 1)))
	if len(r.msgs) == 0 {
		t.Fatal("status-grid 不支持 1x1，应失败")
	}
}

func TestAssertValidAgainstManifest(t *testing.T) {
	m := mustParse(t, goodYAML)
	ok := &report.Report{Status: report.StatusOK, Items: []report.Item{{Key: "box[a]", Type: report.TypeState, State: report.StatusOK}}}
	r := &recorder{TB: t}
	AssertValidAgainstManifest(r, m, ok)
	if len(r.msgs) != 0 {
		t.Fatalf("合法报告不应失败: %v", r.msgs)
	}
	bad := &report.Report{Status: report.StatusOK, Items: []report.Item{{Key: "other", Type: report.TypeState, State: report.StatusOK}}}
	r = &recorder{TB: t}
	AssertValidAgainstManifest(r, m, bad)
	if len(r.msgs) == 0 {
		t.Fatal("未声明的键应失败")
	}
}
