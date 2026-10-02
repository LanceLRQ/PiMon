package kiosk

import (
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const (
	// hubSettingsWait 是首次启动 Chromium 前等待首份设置的时间。
	hubSettingsWait = 3 * time.Second
	// periodicReportInterval 是周期上报的间隔。
	periodicReportInterval = 60 * time.Second
)

// Peripherals 是 kiosk 与外设、系统配置交互用到的外部依赖，测试可替换。
type Peripherals struct {
	WlopmPath string
	Runner    CommandRunner
	// InputDir、UdevDir 用于触摸检测。
	InputDir string
	UdevDir  string
	// OpenInput 打开输入设备做只读旁听。
	OpenInput func(path string) (io.ReadCloser, error)
	// 息屏检查的三处 autostart 与 /proc 根目录。
	UserAutostart    string
	GreeterAutostart string
	SystemAutostart  string
	ProcRoot         string
}

// DefaultPeripherals 返回真实系统路径与实现。
func DefaultPeripherals(getenv func(string) string) Peripherals {
	return Peripherals{
		WlopmPath:        DefaultWlopmPath,
		Runner:           ExecRunner,
		InputDir:         DefaultInputDir,
		UdevDir:          DefaultUdevDataDir,
		OpenInput:        func(path string) (io.ReadCloser, error) { return os.Open(path) },
		UserAutostart:    UserAutostartPath(getenv),
		GreeterAutostart: GreeterAutostartPath,
		SystemAutostart:  SystemAutostartPath,
		ProcRoot:         "/proc",
	}
}

// attachHubLink 把真实的 hub 链路与外设接进 cfg：WebSocket 客户端、wlopm 电源控制、触摸检测与唤醒、
// 息屏检查、周期上报，以及 FillReport 的合并。返回的 bind 须在 New 之后、Run 之前调用，
// 让外设的「状态变化立即上报」能找到守护进程。
func attachHubLink(cfg *Config, p Peripherals) (bind func(*Daemon), err error) {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	var dref atomic.Pointer[Daemon]
	reportNow := func() {
		if d := dref.Load(); d != nil {
			d.ReportNow()
		}
	}
	var mode atomic.Value
	mode.Store("")
	power := NewPower(p.WlopmPath, p.Runner, cfg.Log)

	link, err := NewWSLink(WSConfig{
		HubURL: cfg.HubURL,
		Clock:  cfg.Clock,
		Log:    cfg.Log,
		OnScreenState: func(st model.ScreenState) {
			mode.Store(st.Mode)
			power.Apply(st.Mode)
		},
		// 重新连上后，hub 下发的屏幕状态要无条件应用一次。
		OnConnected: power.Invalidate,
	})
	if err != nil {
		return nil, err
	}
	touch := NewTouchWatcher(TouchConfig{
		Clock:    cfg.Clock,
		Log:      cfg.Log,
		Detect:   func() TouchResult { return DetectTouch(p.InputDir, p.UdevDir) },
		Open:     p.OpenInput,
		Mode:     func() string { return mode.Load().(string) },
		Wake:     link.Wake,
		OnChange: reportNow,
	})
	idle := NewIdleChecker(IdleConfig{
		UserPath:    p.UserAutostart,
		GreeterPath: p.GreeterAutostart,
		SystemPath:  p.SystemAutostart,
		ProcRoot:    p.ProcRoot,
		Clock:       cfg.Clock,
		Log:         cfg.Log,
		OnChange:    reportNow,
	})

	cfg.Link = link
	cfg.SettingsWait = hubSettingsWait
	cfg.ReportInterval = periodicReportInterval
	cfg.Services = append(cfg.Services, touch.Run, idle.Run)
	cfg.FillReport = func(r *model.KioskReport) {
		reportSources{
			Touch: touch.Result,
			Idle:  idle.Last,
			PID: func() int {
				if d := dref.Load(); d != nil {
					return d.ChromiumPID()
				}
				return 0
			},
			RSS: groupRSS,
		}.Fill(r)
	}
	return func(d *Daemon) { dref.Store(d) }, nil
}
