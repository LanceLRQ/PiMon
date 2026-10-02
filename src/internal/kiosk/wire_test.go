package kiosk

import (
	"context"
	"io"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// 端到端：假 hub + 假 Chromium + 假 wlopm + 临时目录里的 udev/autostart/触摸设备，
// 走真实的 WSLink、Power、TouchWatcher、IdleChecker 与守护进程。
func TestAttachHubLink_端到端(t *testing.T) {
	hub := newFakeHub(t)
	root := t.TempDir()
	inputDir, udevDir := filepath.Join(root, "input"), filepath.Join(root, "udev")
	writeFiles(t, inputDir, map[string]string{"event3": ""})
	writeFiles(t, udevDir, map[string]string{"c13:67": udevTouch})
	writeFiles(t, root, map[string]string{"user-autostart": "kanshi &\n" + raspiSwayidle})
	writeFiles(t, filepath.Join(root, "proc"), map[string]string{})

	runner := &fakeRunner{notify: make(chan runCall, 16)}
	var mu sync.Mutex
	var touchPipe *io.PipeWriter
	opened := make(chan struct{}, 1)
	launcher := newFakeLauncher()
	clk := newArmClock(testStart)

	h := newHarness(t, func(c *Config) {
		c.HubURL = hub.srv.URL
		c.Clock = clk
		c.Launcher = launcher
		c.Link = nil
	})
	bind, err := attachHubLink(&h.cfg, Peripherals{
		WlopmPath:     "/usr/bin/wlopm",
		Runner:        runner.run,
		InputDir:      inputDir,
		UdevDir:       udevDir,
		StatRdev:      func(string) (uint32, uint32, error) { return 13, 67, nil },
		ProcRoot:      filepath.Join(root, "proc"),
		UserAutostart: filepath.Join(root, "user-autostart"),
		OpenInput: func(string) (io.ReadCloser, error) {
			r, w := io.Pipe()
			mu.Lock()
			touchPipe = w
			mu.Unlock()
			opened <- struct{}{}
			return r, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.cfg.SettingsWait != 3*time.Second {
		t.Fatalf("SettingsWait=%v", h.cfg.SettingsWait)
	}
	d, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	bind(d)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(waitLimit):
			t.Error("守护进程未退出")
		}
	})

	conn := hub.nextConn(t)
	select {
	case <-opened:
	case <-time.After(waitLimit):
		t.Fatal("触摸设备未被打开")
	}

	// 设置与屏幕状态（关屏）一起到达：Chromium 随之启动，wlopm --off。
	sendJSON(t, conn, map[string]any{
		"type":            "snapshot",
		"build":           "v1",
		"screen_settings": map[string]any{"timezone": "UTC", "screen": map[string]any{"ui_scale": 1}},
		"screen_state":    map[string]any{"mode": "off", "reason": "schedule"},
	})
	launcher.next(t)
	if got := <-runner.notify; !reflect.DeepEqual(got, runCall{"/usr/bin/wlopm", []string{"--off", "*"}}) {
		t.Fatalf("wlopm 调用 %v", got)
	}

	// 上报合并了触摸检测与息屏检查。
	var rep map[string]any
	deadline := time.After(waitLimit)
	for rep == nil {
		select {
		case m := <-hub.msgs:
			k, _ := m["kiosk"].(map[string]any)
			if m["type"] == "kiosk_report" && k["touchscreen"] == true && k["idle_check"] != nil && k["chromium_started_at"] != nil {
				rep = k
			}
		case <-deadline:
			t.Fatal("没有等到完整的 kiosk_report")
		}
	}
	if idle := rep["idle_check"].(map[string]any); idle["user"] != true || idle["greeter"] != false {
		t.Fatalf("idle_check=%v", idle)
	}

	// 关屏时触摸 -> kiosk_wake。
	mu.Lock()
	w := touchPipe
	mu.Unlock()
	if _, err := w.Write(evTouchDown); err != nil {
		t.Fatal(err)
	}
	hub.nextMsg(t, "kiosk_wake")

	// 亮屏后：wlopm --on，触摸不再唤醒。
	sendJSON(t, conn, map[string]any{"type": "patch", "entity": "screen_state", "screen_state": map[string]any{"mode": "on", "reason": "wake"}})
	if got := <-runner.notify; got.args[0] != "--on" {
		t.Fatalf("wlopm 调用 %v", got)
	}
	clk.Advance(10 * time.Second)
	if _, err := w.Write(evTouchDown); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(evSyn); err != nil {
		t.Fatal(err)
	}
	for len(hub.msgs) > 0 {
		if m := <-hub.msgs; m["type"] == "kiosk_wake" {
			t.Fatal("亮屏时不应发 kiosk_wake")
		}
	}
}

func TestAttachHubLink_HubURL无效时报错(t *testing.T) {
	cfg := Config{HubURL: "not-a-url"}
	if _, err := attachHubLink(&cfg, Peripherals{}); err == nil {
		t.Fatal("应报错")
	}
}

func TestDefaultPeripherals(t *testing.T) {
	p := DefaultPeripherals(func(k string) string {
		return map[string]string{"HOME": "/home/u"}[k]
	})
	if p.WlopmPath != DefaultWlopmPath || p.InputDir != DefaultInputDir || p.UdevDir != DefaultUdevDataDir ||
		p.UserAutostart != "/home/u/.config/labwc/autostart" || p.GreeterAutostart != GreeterAutostartPath ||
		p.SystemAutostart != SystemAutostartPath || p.ProcRoot != "/proc" || p.Runner == nil || p.OpenInput == nil {
		t.Fatalf("%+v", p)
	}
}
