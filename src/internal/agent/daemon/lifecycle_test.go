package daemon

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/agent/store"
	"github.com/LanceLRQ/PiMon/src/internal/hub/logging"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// settledGoroutines 等协程数稳定（连续多次采样不变）后返回，作为对比基线。
func settledGoroutines() int {
	last, same := goruntime.NumGoroutine(), 0
	for same < 10 {
		time.Sleep(5 * time.Millisecond)
		if n := goruntime.NumGoroutine(); n == last {
			same++
		} else {
			last, same = n, 0
		}
	}
	return last
}

func dumpOnFail(t *testing.T) {
	if t.Failed() {
		buf := make([]byte, 1<<20)
		t.Logf("%s", buf[:goruntime.Stack(buf, true)])
	}
}

func TestRun_拨入取消后无残留协程(t *testing.T) {
	hub := startStubHub(t, nil)
	// 先跑一轮让替身 hub 的惰性协程就绪，再取基线。
	warm := newHarness(t, hub.config()).start()
	waitUntil(t, "连上", func() bool { return warm.status().Connected })
	warm.stop()
	<-hub.nextSession().Done()
	base := settledGoroutines()

	h := newHarness(t, hub.config()).start()
	waitUntil(t, "连上", func() bool { return h.status().Connected })
	s := hub.nextSession()
	h.stop()
	<-s.Done()
	if r := s.Err(); r.Bye != agent.ReasonShutdown {
		t.Fatalf("退出时应以 shutdown 关闭会话: %+v", r)
	}
	defer dumpOnFail(t)
	waitUntil(t, "协程回落", func() bool { return goruntime.NumGoroutine() <= base })
}

func TestRun_拨出取消后无残留协程(t *testing.T) {
	base := settledGoroutines()
	h, addr, pin := dialOutHarness(t)
	f := pairFake(t, h, addr, pin, nil)
	waitUntil(t, "连上", func() bool { return h.status().Connected })
	must(t, f.sess.Send(&agent.Assign{Version: 1, Instances: []agent.AssignedInstance{
		{ID: "i1", PluginID: "probe-x", IntervalMs: 60000, ConfigHash: "h1"},
	}}))
	waitUntil(t, "实例已下发", func() bool { return h.status().Instances == 1 })
	h.stop()
	<-f.sess.Done()
	defer dumpOnFail(t)
	waitUntil(t, "协程回落", func() bool { return goruntime.NumGoroutine() <= base })
}

func TestRun_每24小时清理30天未出现的state(t *testing.T) {
	h := newHarness(t, &config.Config{Mode: config.ModeDialOut})
	st, err := store.Open(h.paths.DB, store.WithClock(clock.NewFake(t0)))
	must(t, err)
	must(t, st.PutState(context.Background(), "old", "probe-x", "c1", "s"))
	must(t, st.Close())
	exists := func() bool {
		s, err := store.Open(h.paths.DB)
		must(t, err)
		defer func() { _ = s.Close() }()
		_, err = s.State(context.Background(), "old")
		return !errors.Is(err, sql.ErrNoRows)
	}

	h.start()
	h.listenAddr()
	h.clk.Advance(GCInterval - time.Second)
	never(t, "未到 24 小时就清理", func() bool { return !exists() })
	h.clk.Advance(31*24*time.Hour - GCInterval + time.Second)
	waitUntil(t, "GC 删除 30 天未出现的 state", func() bool { return !exists() })
}

func TestNew_缺省依赖不改slog默认(t *testing.T) {
	before := slog.Default()
	d := New(Config{})
	if slog.Default() != before || d.log == nil || d.clk == nil {
		t.Fatal("New 不应修改 slog.Default，且应填好缺省依赖")
	}
	if st := d.Status(); st.State != StateIdle || st.Connected {
		t.Fatalf("运行前状态 = %+v", st)
	}
}

func TestRun_只能调用一次(t *testing.T) {
	h := newHarness(t, &config.Config{Mode: config.ModeDialOut}).start()
	h.listenAddr()
	if err := h.d.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "已在运行") {
		t.Fatalf("重复 Run 应报错，得到 %v", err)
	}
}

func TestLogs_默认条数与上限(t *testing.T) {
	ring := logging.NewRing(3000)
	log := slog.New(ring.Handler(slog.LevelDebug))
	for i := range 2500 {
		log.Info("m", "i", i)
	}
	if got := buildLogs(ring, 0, ""); len(got) != LogsDefaultLimit || got[len(got)-1].Attrs != "i=2499" {
		t.Fatalf("默认应取最近 %d 条，得到 %d 条", LogsDefaultLimit, len(got))
	}
	if got := buildLogs(ring, 5000, ""); len(got) != LogsMaxLimit {
		t.Fatalf("上限应为 %d，得到 %d", LogsMaxLimit, len(got))
	}
	if got := buildLogs(nil, 10, ""); got == nil || len(got) != 0 {
		t.Fatal("没有日志环时返回空切片")
	}
}

func TestLogs_按级别过滤(t *testing.T) {
	ring := logging.NewRing(10)
	log := slog.New(ring.Handler(slog.LevelDebug))
	log.Debug("d")
	log.Info("i")
	log.Warn("w")
	log.Error("e")
	var got []string
	for _, e := range buildLogs(ring, 0, "warn") {
		got = append(got, e.Level+":"+e.Message)
	}
	if strings.Join(got, ",") != "warn:w,error:e" {
		t.Fatalf("按级别过滤 = %v", got)
	}
	if n := len(buildLogs(ring, 0, "")); n != 4 {
		t.Fatalf("不过滤应返回全部，得到 %d", n)
	}
}

func TestLogs_累计超过6MiB时从最旧开始丢(t *testing.T) {
	ring := logging.NewRing(RingCapacity)
	log := slog.New(ring.Handler(slog.LevelDebug))
	big := strings.Repeat("x", 3000)
	for i := range RingCapacity {
		log.Info(big, "i", i, "detail", big)
	}
	got := buildLogs(ring, LogsMaxLimit, "")
	b, err := agent.Encode(&agent.Logs{RequestID: "r", Entries: got})
	must(t, err)
	if len(b) > LogsMaxBytes || len(got) == RingCapacity || len(got) == 0 {
		t.Fatalf("编码后 %d 字节、%d 条", len(b), len(got))
	}
	if !strings.HasPrefix(got[len(got)-1].Attrs, "i=1999 ") {
		t.Fatal("应保留最新的日志、丢弃最旧的")
	}
}
