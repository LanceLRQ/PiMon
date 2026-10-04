package hubself

import (
	"errors"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

type remoteStats struct {
	fakeStats
	st  RemoteBackupStatus
	err error
}

func (r *remoteStats) RemoteBackup() (RemoteBackupStatus, error) { return r.st, r.err }

func TestRemoteBackupItem(t *testing.T) {
	now := t0.Add(90 * time.Second) // boundWith 的假时钟起点
	at := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	cases := []struct {
		name      string
		st        RemoteBackupStatus
		state     report.Status
		text      string
		repStatus report.Status
	}{
		{"最近成功且未超 48 小时", RemoteBackupStatus{Enabled: true, LastAttempt: at(time.Hour), LastSuccess: at(time.Hour)},
			report.StatusOK, textBackupOK, report.StatusOK},
		{"48 小时整点算正常", RemoteBackupStatus{Enabled: true, LastAttempt: at(48 * time.Hour), LastSuccess: at(48 * time.Hour)},
			report.StatusOK, textBackupOK, report.StatusOK},
		{"超过 48 小时一纳秒", RemoteBackupStatus{Enabled: true, LastAttempt: at(48*time.Hour + 1), LastSuccess: at(48*time.Hour + 1)},
			report.StatusWarning, textBackupStale, report.StatusWarning},
		{"最近一次失败", RemoteBackupStatus{Enabled: true, LastAttempt: at(time.Minute), LastSuccess: at(time.Hour), LastFailed: true},
			report.StatusCritical, textBackupFailed, report.StatusCritical},
		{"从未成功且失败", RemoteBackupStatus{Enabled: true, LastAttempt: at(time.Minute), LastFailed: true},
			report.StatusCritical, textBackupFailed, report.StatusCritical},
		{"从未上传", RemoteBackupStatus{Enabled: true},
			report.StatusWarning, textBackupNever, report.StatusWarning},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, clk := boundWith(t, &remoteStats{fakeStats: fakeStats{started: t0, dir: "/d"}, st: c.st}, newFakeSys())
			rep := collect(t, p, clk, nil)
			it := rep.Find(keyRemoteBackup)
			if it == nil {
				t.Fatal("启用时应输出 hub.remote_backup")
			}
			if it.Type != report.TypeState || it.State != c.state || it.Text != c.text {
				t.Fatalf("item = %+v，期望 %s/%s", it, c.state, c.text)
			}
			if rep.Status != c.repStatus {
				t.Fatalf("报告状态 = %s，期望 %s", rep.Status, c.repStatus)
			}
		})
	}
}

// 用假时钟推进跨过 48 小时边界：整点仍正常，再过一秒变为 warning。
func TestRemoteBackupBoundaryWithFakeClock(t *testing.T) {
	st := &remoteStats{fakeStats: fakeStats{started: t0, dir: "/d"}}
	p, clk := boundWith(t, st, newFakeSys())
	success := clk.Now()
	st.st = RemoteBackupStatus{Enabled: true, LastAttempt: &success, LastSuccess: &success}
	clk.Advance(48 * time.Hour)
	if it := collect(t, p, clk, nil).Find(keyRemoteBackup); it.State != report.StatusOK {
		t.Fatalf("48 小时整点：%+v", it)
	}
	clk.Advance(time.Second)
	if it := collect(t, p, clk, nil).Find(keyRemoteBackup); it.State != report.StatusWarning || it.Text != textBackupStale {
		t.Fatalf("超过 48 小时：%+v", it)
	}
}

func TestRemoteBackupItemOmittedOrUnknown(t *testing.T) {
	// 未启用：不输出。
	p, clk := boundWith(t, &remoteStats{fakeStats: fakeStats{started: t0, dir: "/d"}}, newFakeSys())
	if collect(t, p, clk, nil).Find(keyRemoteBackup) != nil {
		t.Fatal("未启用不应输出该数据项")
	}
	// 来源未实现可选接口：不输出。
	p, clk = boundWith(t, &fakeStats{started: t0, dir: "/d"}, newFakeSys())
	if collect(t, p, clk, nil).Find(keyRemoteBackup) != nil {
		t.Fatal("来源未接入不应输出该数据项")
	}
	// 读取状态失败：未知，不拉高报告状态。
	p, clk = boundWith(t, &remoteStats{fakeStats: fakeStats{started: t0, dir: "/d"}, err: errors.New("db")}, newFakeSys())
	rep := collect(t, p, clk, nil)
	it := rep.Find(keyRemoteBackup)
	if it == nil || it.State != report.StatusUnknown || it.Error == "" {
		t.Fatalf("读取失败应为未知：%+v", it)
	}
	if rep.Status != report.StatusOK {
		t.Fatalf("未知不应拉高状态：%s", rep.Status)
	}
}

func TestManifestDeclaresRemoteBackup(t *testing.T) {
	p := New(okDisk(1, 2))
	for _, o := range p.Manifest().Outputs {
		if o.Key == keyRemoteBackup {
			if o.Type != report.TypeState {
				t.Fatalf("类型 = %s", o.Type)
			}
			return
		}
	}
	t.Fatal("manifest outputs 缺少 hub.remote_backup")
}
