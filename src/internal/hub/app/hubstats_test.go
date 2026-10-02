package app

import (
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func TestKioskRSSFromStatus(t *testing.T) {
	rss := int64(123456)
	cases := []struct {
		name string
		k    *model.KioskStatus
		want int64
		ok   bool
	}{
		{"从未上报", nil, 0, false},
		{"在线有值", &model.KioskStatus{Online: true, ChromiumRSSBytes: &rss}, rss, true},
		{"离线", &model.KioskStatus{Online: false, ChromiumRSSBytes: &rss}, 0, false},
		{"值为空", &model.KioskStatus{Online: true}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := kioskRSS(model.ScreenStatus{Kiosk: c.k})
			if got != c.want || ok != c.ok {
				t.Errorf("得到 %d,%v，期望 %d,%v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestHubStatsKioskRSSUnwired(t *testing.T) {
	if _, ok := (hubStats{}).KioskChromiumRSS(); ok {
		t.Error("来源未接入应为未知")
	}
}

func TestHubStats在线agent数(t *testing.T) {
	if n := (hubStats{}).OnlineAgents(); n != 0 {
		t.Errorf("未接入时 OnlineAgents = %d, 期望 0", n)
	}
	clk := clock.NewFake(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	p := devices.NewPresence(clk, clk.Now(), nil)
	defer p.Close()
	s := hubStats{onlineAgents: p.Online}
	p.Connected("a")
	p.Connected("b")
	if n := s.OnlineAgents(); n != 2 {
		t.Errorf("OnlineAgents = %d, 期望 2", n)
	}
	p.Disconnected("a", clk.Now())
	if n := s.OnlineAgents(); n != 1 {
		t.Errorf("断开后 OnlineAgents = %d, 期望 1", n)
	}
	p.Forget("b")
	if n := s.OnlineAgents(); n != 0 {
		t.Errorf("Forget 后 OnlineAgents = %d, 期望 0", n)
	}
}

func TestOpen装配在线状态并随Close停止(t *testing.T) {
	a := openApp(t, testConfig(t))
	if a.presence == nil || a.devices == nil {
		t.Fatal("Open 应装配 devices 与 presence")
	}
	if a.presence.Online() != 0 {
		t.Errorf("Online = %d", a.presence.Online())
	}
	a.presence.Connected("x")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Close 后不再起计时协程：断开只更新状态，不 panic，也不会再转为 offline。
	a.presence.Disconnected("x", time.Now())
	if st, _ := a.presence.State("x"); st != model.PresenceDisconnected {
		t.Errorf("Close 后断开状态 = %q, 期望 disconnected", st)
	}
}
