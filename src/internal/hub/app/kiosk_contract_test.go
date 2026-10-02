package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/kiosk"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// contractSink 是 kiosk.LinkSink 的测试实现：每次重连前从令牌文件重读令牌。
type contractSink struct {
	tokenPath  string
	tokenReads atomic.Int32
	connected  chan struct{}
	settings   chan ui.ScreenSettings
	builds     chan string
}

func (s *contractSink) OnConnected()                   { s.connected <- struct{}{} }
func (s *contractSink) OnSettings(v ui.ScreenSettings) { s.settings <- v }
func (s *contractSink) OnBuild(b string)               { s.builds <- b }
func (s *contractSink) ReadToken() (string, error) {
	s.tokenReads.Add(1)
	raw, err := os.ReadFile(s.tokenPath)
	return strings.TrimSpace(string(raw)), err
}

func recvOrFail[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("等待%s超时", what)
		var zero T
		return zero
	}
}

// 契约测试：真实的 kiosk.WSLink 连真实的 hub（Bearer 屏幕令牌），两端协议口径一致。
func TestKioskWSLinkAgainstRealHub(t *testing.T) {
	f := newScreenFixture(t)
	sink := &contractSink{
		tokenPath: f.a.cfg.ScreenTokenPath(),
		connected: make(chan struct{}, 8),
		settings:  make(chan ui.ScreenSettings, 8),
		builds:    make(chan string, 8),
	}
	states := make(chan model.ScreenState, 16)
	link, err := kiosk.NewWSLink(kiosk.WSConfig{
		HubURL:        f.base,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnScreenState: func(s model.ScreenState) { states <- s },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { link.Run(ctx, sink); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("WSLink.Run 未退出")
		}
	})

	// 握手后收到 snapshot：build、settings、screen_state。
	recvOrFail(t, sink.connected, "连接")
	if b := recvOrFail(t, sink.builds, "build"); b != "1.0.0" {
		t.Fatalf("build = %q", b)
	}
	if s := recvOrFail(t, sink.settings, "settings"); s.Timezone == "" {
		t.Fatalf("settings = %+v", s)
	}
	if st := recvOrFail(t, states, "screen_state"); st.Mode != model.ScreenModeOn {
		t.Fatalf("screen_state = %+v", st)
	}

	// kiosk_report 写入 ScreenStatus.kiosk。
	touch := true
	link.Report(model.KioskReport{Version: "1.0.0", Restarts: 2, Touchscreen: &touch})
	eventually(t, "上报入库", func() bool {
		k := f.status().Kiosk
		return k != nil && k.Online && k.Version == "1.0.0" && k.Restarts == 2 && k.Touchscreen != nil && *k.Touchscreen
	})

	// 关屏经 patch 到达 kiosk；kiosk_wake 让屏幕状态唤醒并推回 kiosk。
	if resp, data := call(t, f.admin, f.base, "POST", "/api/screen/control", model.ScreenControlRequest{Action: "off"}); resp.StatusCode != 200 {
		t.Fatalf("off = %d %s", resp.StatusCode, data)
	}
	for st := recvOrFail(t, states, "关屏 patch"); st.Mode != model.ScreenModeOff; st = recvOrFail(t, states, "关屏 patch") {
	}
	if !link.Wake() {
		t.Fatal("已连接时 Wake 应已投递")
	}
	for st := recvOrFail(t, states, "唤醒 patch"); st.Mode != model.ScreenModeOn; st = recvOrFail(t, states, "唤醒 patch") {
	}
	if st := f.a.screenState.State(); st.Mode != model.ScreenModeOn || st.Reason != model.ScreenReasonWake {
		t.Fatalf("hub 屏幕状态应为唤醒: %+v", st)
	}

	// 令牌轮换：连接被断开，kiosk 重读新令牌后重连成功，且上报继续有效。
	reads := sink.tokenReads.Load()
	if resp, data := call(t, f.admin, f.base, "POST", "/api/screen/token/reset", nil); resp.StatusCode >= 300 {
		t.Fatalf("reset = %d %s", resp.StatusCode, data)
	}
	recvOrFail(t, sink.connected, "令牌轮换后重连")
	if sink.tokenReads.Load() <= reads {
		t.Fatal("重连前应重新读取令牌文件")
	}
	link.Report(model.KioskReport{Version: "1.0.0", Restarts: 5})
	eventually(t, "重连后上报入库", func() bool {
		k := f.status().Kiosk
		return k != nil && k.Online && k.Restarts == 5
	})
}
