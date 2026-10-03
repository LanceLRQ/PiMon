package docker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/plugintest"
)

var baseTime = time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)

// fakeEngine 回放 testdata 里的 Engine API 响应，并记录收到的请求路径。
type fakeEngine struct {
	mu         sync.Mutex
	paths      []string
	apiVersion string // 为空则不返回 API-Version 头
	failInspec map[string]bool
	onInspect  func()
}

func (f *fakeEngine) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *fakeEngine) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		p := r.URL.Path
		if i := strings.Index(p, "/_ping"); i >= 0 {
			if f.apiVersion != "" {
				w.Header().Set("API-Version", f.apiVersion)
			}
			_, _ = w.Write(readTestdata(t, "ping.txt"))
			return
		}
		switch {
		case strings.HasSuffix(p, "/containers/json"):
			body := readTestdata(t, "list.json")
			if r.URL.Query().Get("all") == "0" {
				body = []byte(`[{"Id":"aaa1","Names":["/web-1"],"State":"running"},{"Id":"bbb2","Names":["/db"],"State":"running"}]`)
			}
			_, _ = w.Write(body)
		case strings.Contains(p, "/containers/") && strings.HasSuffix(p, "/json"):
			id := strings.TrimSuffix(p[strings.LastIndex(p[:len(p)-5], "/")+1:], "/json")
			if f.onInspect != nil {
				f.onInspect()
			}
			if f.failInspec[id] {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(readTestdata(t, "inspect-"+id+".json"))
		default:
			http.NotFound(w, r)
		}
	})
}

// startEngine 在短路径的 unix socket 上起假服务（macOS 限制 socket 路径长度）。
func startEngine(t *testing.T, f *fakeEngine) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(f.handler(t))
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return sock
}

func collect(t *testing.T, cfgMap map[string]any, now time.Time) (*report.Report, error) {
	t.Helper()
	return New().Collect(context.Background(), runtime.Input{Config: cfgMap, Clock: clock.NewFake(now)})
}

func items(rep *report.Report) map[string]report.Item {
	m := map[string]report.Item{}
	for _, it := range rep.Items {
		m[it.Key] = it
	}
	return m
}

func TestManifest与小组件尺寸(t *testing.T) {
	plugintest.AssertWidgetSizes(t, New().Manifest())
}

func TestCollect各类容器状态(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1") // Transport 必须忽略代理环境变量
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	f := &fakeEngine{apiVersion: "1.52"}
	rep, err := collect(t, map[string]any{"socket": startEngine(t, f)}, baseTime)
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, New().Manifest(), rep)
	m := items(rep)
	want := map[string]report.Status{
		"container[web-1]": report.StatusOK,       // 运行且健康
		"container[db]":    report.StatusWarning,  // 健康检查失败
		"container[job]":   report.StatusUnknown,  // 退出码 0，正常停止
		"container[crash]": report.StatusCritical, // 退出码 1
		"container[loop]":  report.StatusCritical, // 重启循环
		"container[web-2]": report.StatusOK,       // 刚启动，健康检查 starting 未超时
	}
	for k, st := range want {
		it, ok := m[k]
		if !ok {
			t.Fatalf("缺少 %s，现有 %v", k, rep.Items)
		}
		if it.State != st {
			t.Errorf("%s state = %s，期望 %s（text=%q）", k, it.State, st, it.Text)
		}
		if it.Text == "" {
			t.Errorf("%s 没有 text", k)
		}
	}
	if v := m["containers.running"].Value; v == nil || *v != 3 {
		t.Errorf("running = %v，期望 3", v)
	}
	if v := m["containers.total"].Value; v == nil || *v != 6 {
		t.Errorf("total = %v，期望 6", v)
	}
	if rep.Status != report.StatusCritical {
		t.Errorf("报告状态 = %s，期望 critical", rep.Status)
	}
	if !strings.Contains(rep.Summary, "运行 3 / 共 6，异常 3") {
		t.Errorf("summary = %q", rep.Summary)
	}
}

func TestStarting超过阈值才告警(t *testing.T) {
	f := &fakeEngine{apiVersion: "1.52"}
	sock := startEngine(t, f)
	rep, err := collect(t, map[string]any{"socket": sock}, baseTime.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if it := items(rep)["container[web-2]"]; it.State != report.StatusWarning {
		t.Errorf("starting 6 分钟应为 warning，得到 %s（%s）", it.State, it.Text)
	}
}

func TestStarting阈值取StartPeriod与5分钟较大者(t *testing.T) {
	cases := []struct {
		period time.Duration
		age    time.Duration
		want   report.Status
	}{
		{0, 4 * time.Minute, report.StatusOK},
		{0, 6 * time.Minute, report.StatusWarning},
		{10 * time.Minute, 6 * time.Minute, report.StatusOK},
		{10 * time.Minute, 11 * time.Minute, report.StatusWarning},
	}
	for _, c := range cases {
		if got := startingState(c.age, c.period); got != c.want {
			t.Errorf("period=%v age=%v: %s，期望 %s", c.period, c.age, got, c.want)
		}
	}
}

func TestExited退出码判定(t *testing.T) {
	cases := []struct {
		code int
		oom  bool
		want report.Status
	}{
		{0, false, report.StatusUnknown},
		{143, false, report.StatusUnknown},
		{137, false, report.StatusUnknown},
		{137, true, report.StatusCritical},
		{0, true, report.StatusCritical},
		{1, false, report.StatusCritical},
		{255, false, report.StatusCritical},
	}
	for _, c := range cases {
		code := c.code
		st, _ := exitedState(&code, c.oom)
		if st != c.want {
			t.Errorf("code=%d oom=%v: %s，期望 %s", c.code, c.oom, st, c.want)
		}
	}
}

func TestName过滤(t *testing.T) {
	f := &fakeEngine{apiVersion: "1.52"}
	rep, err := collect(t, map[string]any{"socket": startEngine(t, f), "names": []any{"web-*", "db"}}, baseTime)
	if err != nil {
		t.Fatal(err)
	}
	m := items(rep)
	for _, k := range []string{"container[web-1]", "container[web-2]", "container[db]"} {
		if _, ok := m[k]; !ok {
			t.Errorf("缺少 %s", k)
		}
	}
	if _, ok := m["container[crash]"]; ok {
		t.Error("crash 应被过滤")
	}
	if v := m["containers.total"].Value; v == nil || *v != 3 {
		t.Errorf("total = %v，期望 3", v)
	}
	for _, p := range f.seen() {
		if strings.Contains(p, "ddd4") {
			t.Errorf("被过滤的容器不应请求详情: %s", p)
		}
	}
}

func TestInclude_stopped关闭只请求运行中(t *testing.T) {
	f := &fakeEngine{apiVersion: "1.52"}
	rep, err := collect(t, map[string]any{"socket": startEngine(t, f), "include_stopped": false}, baseTime)
	if err != nil {
		t.Fatal(err)
	}
	var listed bool
	for _, p := range f.seen() {
		if strings.Contains(p, "/containers/json") {
			listed = true
			if !strings.Contains(p, "all=0") {
				t.Errorf("应使用 all=0: %s", p)
			}
		}
	}
	if !listed {
		t.Fatal("没有请求列表")
	}
	if v := items(rep)["containers.total"].Value; v == nil || *v != 2 {
		t.Errorf("total = %v，期望 2", v)
	}
}

func TestAPI版本协商(t *testing.T) {
	cases := []struct {
		header string
		prefix string
	}{
		{"1.52", "/v1.52/"},
		{"1.44", "/v1.44/"},
		{"1.99", "/v1.52/"},
		{"", "/containers/json"},
		{"abc", "/containers/json"},
	}
	for _, c := range cases {
		f := &fakeEngine{apiVersion: c.header}
		if _, err := collect(t, map[string]any{"socket": startEngine(t, f)}, baseTime); err != nil {
			t.Fatalf("header=%q: %v", c.header, err)
		}
		var ok bool
		for _, p := range f.seen() {
			if strings.Contains(p, "/containers/json") {
				ok = strings.HasPrefix(p, c.prefix)
			}
		}
		if !ok {
			t.Errorf("header=%q: 请求路径 %v 不以 %q 开头", c.header, f.seen(), c.prefix)
		}
	}
}

func TestSocket不存在(t *testing.T) {
	_, err := collect(t, map[string]any{"socket": filepath.Join(t.TempDir(), "none.sock")}, baseTime)
	if err == nil || !strings.Contains(err.Error(), "docker 组") || !strings.Contains(err.Error(), "近似 root") {
		t.Fatalf("err = %v", err)
	}
}

func TestSocket权限不足(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700); _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	_, err = collect(t, map[string]any{"socket": sock}, baseTime)
	if err == nil || !strings.Contains(err.Error(), "权限") || !strings.Contains(err.Error(), "docker 组") {
		t.Fatalf("err = %v", err)
	}
}

func TestSocket路径不是socket(t *testing.T) {
	p := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, map[string]any{"socket": p}, baseTime); err == nil {
		t.Fatal("普通文件应返回错误")
	}
}

func TestInspect失败只影响该条目(t *testing.T) {
	f := &fakeEngine{apiVersion: "1.52", failInspec: map[string]bool{"bbb2": true}}
	rep, err := collect(t, map[string]any{"socket": startEngine(t, f)}, baseTime)
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, New().Manifest(), rep)
	it := items(rep)["container[db]"]
	if it.Error == "" || it.State != report.StatusUnknown {
		t.Errorf("db 应为 unknown 且带 error: %+v", it)
	}
	if items(rep)["container[web-1]"].State != report.StatusOK {
		t.Error("其他容器不受影响")
	}
	if rep.Status != report.StatusCritical { // 样本里本就有 crash/loop 为 critical
		t.Errorf("报告状态 = %s", rep.Status)
	}
	rep2, err := collect(t, map[string]any{"socket": startEngine(t, &fakeEngine{apiVersion: "1.52", failInspec: map[string]bool{"bbb2": true}}), "names": []any{"db", "web-1"}}, baseTime)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Status != report.StatusWarning {
		t.Errorf("inspect 失败应抬到 warning，得到 %s", rep2.Status)
	}
	if !strings.Contains(rep2.Summary, "异常 1") {
		t.Errorf("summary = %q", rep2.Summary)
	}
}

func TestInspect期间ctx取消返回错误(t *testing.T) {
	f := &fakeEngine{apiVersion: "1.52"}
	sock := startEngine(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	f.onInspect = cancel
	rep, err := New().Collect(ctx, runtime.Input{Config: map[string]any{"socket": sock}, Clock: clock.NewFake(baseTime)})
	if err == nil || rep != nil {
		t.Fatalf("应返回错误而非报告: rep=%v err=%v", rep, err)
	}
}

func TestExited缺退出码判unknown(t *testing.T) {
	st, txt := exitedState(nil, false)
	if st != report.StatusUnknown || !strings.Contains(txt, "缺失") {
		t.Errorf("%s %q", st, txt)
	}
	var d inspectResult
	if err := json.Unmarshal([]byte(`{"State":{"Status":"exited"}}`), &d); err != nil {
		t.Fatal(err)
	}
	if st, _ := classify(d, baseTime); st != report.StatusUnknown {
		t.Errorf("缺 ExitCode 应为 unknown，得到 %s", st)
	}
	if st, _ := exitedState(nil, true); st != report.StatusCritical {
		t.Errorf("OOM 仍应 critical，得到 %s", st)
	}
}

func TestName过滤模式非法(t *testing.T) {
	f := &fakeEngine{apiVersion: "1.52"}
	_, err := collect(t, map[string]any{"socket": startEngine(t, f), "names": []any{"web-*", "["}}, baseTime)
	if err == nil || !strings.Contains(err.Error(), "「[」") {
		t.Fatalf("err = %v", err)
	}
}

func TestSocket默认值(t *testing.T) {
	if got := socketPath(nil); got != "/var/run/docker.sock" {
		t.Errorf("默认 socket = %q", got)
	}
}

// 本机有 Docker 时做只读冒烟：只请求 _ping、列表与详情，不改动任何容器。
func TestDocker本机冒烟(t *testing.T) {
	const sock = "/var/run/docker.sock"
	if _, err := os.Stat(sock); err != nil {
		t.Skip("本机没有 Docker socket")
	}
	rep, err := collect(t, map[string]any{"socket": sock}, time.Now())
	if err != nil {
		t.Skipf("本机 Docker 不可读: %v", err)
	}
	plugintest.AssertValidAgainstManifest(t, New().Manifest(), rep)
	if items(rep)["containers.total"].Value == nil {
		t.Error("缺少 containers.total")
	}
}
