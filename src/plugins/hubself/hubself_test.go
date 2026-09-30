package hubself

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

type fakeStats struct {
	writeErrs int64
	agents    int
	screens   bool
	pushFails int64
	started   time.Time
	dir       string
}

func (f *fakeStats) WriteErrors() int64   { return f.writeErrs }
func (f *fakeStats) OnlineAgents() int    { return f.agents }
func (f *fakeStats) ScreenOnline() bool   { return f.screens }
func (f *fakeStats) PushFailures() int64  { return f.pushFails }
func (f *fakeStats) StartedAt() time.Time { return f.started }
func (f *fakeStats) DataDir() string      { return f.dir }

var t0 = time.Date(2026, 9, 28, 22, 47, 0, 0, time.UTC)

func newBound(t *testing.T, st *fakeStats, disk DiskFunc) (*Plugin, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(t0.Add(90 * time.Second))
	p := New(disk)
	if st != nil {
		p.Bind(st)
	}
	return p, clk
}

func okDisk(free, total uint64) DiskFunc {
	return func(string) (uint64, uint64, error) { return free, total, nil }
}

func collect(t *testing.T, p *Plugin, clk clock.Clock, secrets map[string]string) *report.Report {
	t.Helper()
	rep, err := p.Collect(context.Background(), runtime.Input{Clock: clk, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	if err := rep.Validate(nil); err != nil {
		t.Fatalf("报告应通过校验: %v", err)
	}
	return rep
}

func num(t *testing.T, rep *report.Report, key string) float64 {
	t.Helper()
	it := rep.Find(key)
	if it == nil || it.Value == nil {
		t.Fatalf("缺少数值项 %s: %+v", key, rep.Items)
	}
	return *it.Value
}

func TestRegisteredAndManifest(t *testing.T) {
	src, ok := runtime.Builtin("hub-self")
	if !ok {
		t.Fatal("hub-self 应在 init 中注册")
	}
	var f *schema.Field
	for i := range src.Manifest().ConfigSchema {
		if src.Manifest().ConfigSchema[i].Key == "heartbeat_url" {
			f = &src.Manifest().ConfigSchema[i]
		}
	}
	if f == nil || f.Type != schema.TypeSecretURL || f.Required {
		t.Fatalf("heartbeat_url 应为可选 secret_url: %+v", f)
	}
}

func TestUnboundOmitsItems(t *testing.T) {
	p, clk := newBound(t, nil, okDisk(1, 2))
	rep := collect(t, p, clk, nil)
	if len(rep.Items) != 0 || rep.Status != report.StatusOK {
		t.Fatalf("未绑定统计时不应输出数据项: %+v", rep)
	}
	if len(rep.Events) != 0 {
		t.Fatalf("未绑定时不应产生重启事件: %+v", rep.Events)
	}
}

func TestItemsFromStats(t *testing.T) {
	st := &fakeStats{writeErrs: 0, agents: 0, screens: false, pushFails: 0, started: t0, dir: "/data"}
	var gotPath string
	p, clk := newBound(t, st, func(path string) (uint64, uint64, error) {
		gotPath = path
		return 40, 100, nil
	})
	rep := collect(t, p, clk, nil)
	if gotPath != "/data" {
		t.Errorf("磁盘查询应用数据目录: %q", gotPath)
	}
	if num(t, rep, "hub.write_errors") != 0 || num(t, rep, "hub.push_failures") != 0 || num(t, rep, "hub.agents_online") != 0 {
		t.Errorf("计数项应为 0: %+v", rep.Items)
	}
	if num(t, rep, "hub.uptime") != 90 {
		t.Errorf("运行时长应为 90 秒: %v", num(t, rep, "hub.uptime"))
	}
	d := rep.Find("hub.disk_free")
	if d == nil || d.Type != report.TypeQuota || *d.Remaining != 40 || *d.Total != 100 || *d.Used != 60 || *d.RemainingPct != 40 {
		t.Fatalf("磁盘项错误: %+v", d)
	}
	s := rep.Find("hub.screens_online")
	if s == nil || s.Type != report.TypeState || s.State == report.StatusOK {
		t.Fatalf("屏幕离线不应为 ok: %+v", s)
	}
	if rep.Status != report.StatusOK {
		t.Errorf("一切正常时 status 应为 ok: %v", rep.Status)
	}
}

func TestStatusEscalation(t *testing.T) {
	st := &fakeStats{writeErrs: 3, started: t0, dir: "/d"}
	p, clk := newBound(t, st, okDisk(40, 100))
	if rep := collect(t, p, clk, nil); rep.Status != report.StatusWarning {
		t.Errorf("写库错误应升为 warning: %v", rep.Status)
	}
	st.writeErrs = 0
	p2, clk2 := newBound(t, st, okDisk(4, 100))
	if rep := collect(t, p2, clk2, nil); rep.Status != report.StatusCritical {
		t.Errorf("剩余空间不足 5%% 应为 critical: %v", rep.Status)
	}
	p3, clk3 := newBound(t, st, okDisk(8, 100))
	if rep := collect(t, p3, clk3, nil); rep.Status != report.StatusWarning {
		t.Errorf("剩余空间不足 10%% 应为 warning: %v", rep.Status)
	}
}

func TestDiskErrorKeepsItemWithError(t *testing.T) {
	st := &fakeStats{started: t0, dir: "/d"}
	p, clk := newBound(t, st, func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs 失败") })
	rep := collect(t, p, clk, nil)
	d := rep.Find("hub.disk_free")
	if d == nil || d.Error == "" || d.RemainingPct != nil {
		t.Fatalf("查询失败应保留带 error 的条目而不是 0: %+v", d)
	}
}

func TestRestartEventOnlyInFirstReport(t *testing.T) {
	st := &fakeStats{started: t0, dir: "/d"}
	p, clk := newBound(t, st, okDisk(50, 100))
	first := collect(t, p, clk, nil)
	if len(first.Events) != 1 || first.Events[0].Type != "hub.restarted" || first.Events[0].ID == "" ||
		first.Events[0].At != t0.UnixMilli() {
		t.Fatalf("首份报告应带 hub.restarted 事件: %+v", first.Events)
	}
	if second := collect(t, p, clk, nil); len(second.Events) != 0 {
		t.Fatalf("重启事件只出现一次: %+v", second.Events)
	}
}

func TestHeartbeat(t *testing.T) {
	var hits atomic.Int32
	var status atomic.Int32
	status.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("心跳应为 GET: %s", r.Method)
		}
		hits.Add(1)
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()
	st := &fakeStats{started: t0, dir: "/d"}
	p, clk := newBound(t, st, okDisk(50, 100))
	secrets := map[string]string{"heartbeat_url": srv.URL + "/ping?token=secret-token"}

	rep := collect(t, p, clk, secrets)
	if hits.Load() != 1 || rep.Status != report.StatusOK {
		t.Fatalf("成功心跳: hits=%d status=%v", hits.Load(), rep.Status)
	}
	collect(t, p, clk, nil)
	if hits.Load() != 1 {
		t.Fatal("未配置地址时不应发请求")
	}

	status.Store(500)
	rep = collect(t, p, clk, secrets)
	if rep.Status != report.StatusWarning || rep.Summary == "" {
		t.Fatalf("心跳失败应 warning 并写 summary: %+v", rep)
	}
	if rep.Find("hub.uptime") == nil {
		t.Fatal("心跳失败时数据项照常输出")
	}
	if containsSecret(rep.Summary, "secret-token") {
		t.Fatalf("summary 不得含心跳地址密钥: %s", rep.Summary)
	}
}

func TestHeartbeatUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/?k=secret-token"
	srv.Close()
	st := &fakeStats{started: t0, dir: "/d"}
	p, clk := newBound(t, st, okDisk(50, 100))
	rep := collect(t, p, clk, map[string]string{"heartbeat_url": url})
	if rep.Status != report.StatusWarning || containsSecret(rep.Summary, "secret-token") {
		t.Fatalf("连接失败应 warning 且不泄露地址: %+v", rep)
	}
}

func containsSecret(s, sub string) bool { return strings.Contains(s, sub) }

func TestStatDiskReal(t *testing.T) {
	free, total, err := statDisk(t.TempDir())
	if err != nil || total == 0 || free > total {
		t.Fatalf("真实磁盘查询异常: %d %d %v", free, total, err)
	}
}
