package instances

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

// contentHash 读实例行的 config_hash（下发时随 RemoteSpec.ContentHash 记录）。
func (mf *machineFx) contentHash(t *testing.T, id string) string {
	t.Helper()
	var h string
	must(t, mf.db.QueryRow(`SELECT config_hash FROM plugin_instances WHERE id = ?`, id).Scan(&h))
	return h
}

func TestRun远程成功入库且不重新下发(t *testing.T) {
	mf := newMachineFx(t)
	rec := &recNotify{}
	mf.svc.OnChange(rec.add)
	d, err := mf.createOn(devServer, "agentonly", "远程", map[string]any{"host": "a"})
	must(t, err)
	mf.remote.take()
	before := rec.count(d.ID)

	mf.remote.setRun(&report.Report{Status: report.StatusOK, Summary: "远程成功", State: "不应保留"}, nil)
	res, err := mf.svc.Run(bg, d.ID)
	must(t, err)
	if res.Report == nil || res.Report.Summary != "远程成功" || res.Report.State != "" {
		t.Fatalf("返回的报告 = %+v", res.Report)
	}
	v, err := mf.svc.View(bg, d.ID)
	must(t, err)
	if v.Failures != 0 || v.LastError != "" {
		t.Fatalf("成功后 failures=%d last_error=%q", v.Failures, v.LastError)
	}
	dt, err := mf.svc.Get(bg, d.ID)
	must(t, err)
	if dt.Report == nil || dt.Report.Summary != "远程成功" {
		t.Fatalf("当前报告 = %+v", dt.Report)
	}
	if _, st := mf.svc.lastFor(d.ID); st != "" {
		t.Fatal("远程实例不应在 hub 保存插件私有 state")
	}
	if mf.hist.count() != 1 {
		t.Fatalf("成功结果应写历史，得到 %d 条", mf.hist.count())
	}
	if rec.count(d.ID) <= before {
		t.Fatal("结果入库应通知实例变化（UI 推送）")
	}
	if got := mf.remote.take(); len(got) != 0 {
		t.Fatalf("测试运行不应触发重新下发: %v", got)
	}
}

func TestRun远程失败入库(t *testing.T) {
	mf := newMachineFx(t)
	d, err := mf.createOn(devServer, "agentonly", "远程", map[string]any{"host": "a"})
	must(t, err)

	mf.remote.setRun(nil, fmt.Errorf("%w: 设备上插件超时", runtime.ErrTimeout))
	if _, err := mf.svc.Run(bg, d.ID); !errors.Is(err, runtime.ErrTimeout) {
		t.Fatalf("应返回超时: %v", err)
	}
	v, err := mf.svc.View(bg, d.ID)
	must(t, err)
	if v.Failures != 1 || !strings.Contains(v.LastError, "设备上插件超时") {
		t.Fatalf("超时结果应入库: failures=%d last_error=%q", v.Failures, v.LastError)
	}

	mf.remote.setRun(nil, fmt.Errorf("%w: exit 1", runtime.ErrFailed))
	if _, err := mf.svc.Run(bg, d.ID); !errors.Is(err, runtime.ErrFailed) {
		t.Fatalf("应返回失败: %v", err)
	}
	v, _ = mf.svc.View(bg, d.ID)
	if v.Failures != 2 {
		t.Fatalf("失败结果应入库: failures=%d", v.Failures)
	}
}

func TestRun远程不入库的结果(t *testing.T) {
	mf := newMachineFx(t)
	d, err := mf.createOn(devServer, "agentonly", "远程", map[string]any{"host": "a"})
	must(t, err)

	incompat := &DeviceIncompatibleError{Reason: "version_too_old", RequiredMin: 2, HubVersion: "0.3.0"}
	cases := []struct {
		name string
		err  error
		is   error
	}{
		{"离线", fmt.Errorf("%w: 会话已断开", ErrDeviceOffline), ErrDeviceOffline},
		{"不兼容", incompat, ErrDeviceIncompatible},
		{"忙", ErrRunBusy, ErrRunBusy},
		{"请求超时", NotRecorded(fmt.Errorf("%w: 设备未在限定时间内应答", runtime.ErrTimeout)), runtime.ErrTimeout},
		{"尚未下发", NotRecorded(fmt.Errorf("%w: 实例尚未下发到设备", runtime.ErrFailed)), runtime.ErrFailed},
		{"调用方取消", context.Canceled, context.Canceled},
	}
	for _, c := range cases {
		mf.remote.setRun(nil, c.err)
		_, err := mf.svc.Run(bg, d.ID)
		if !errors.Is(err, c.is) {
			t.Fatalf("%s: 错误 = %v", c.name, err)
		}
		v, _ := mf.svc.View(bg, d.ID)
		if v.Failures != 0 || v.LastError != "" {
			t.Fatalf("%s: 不应入库，failures=%d last_error=%q", c.name, v.Failures, v.LastError)
		}
	}
	var ie *DeviceIncompatibleError
	mf.remote.setRun(nil, incompat)
	if _, err := mf.svc.Run(bg, d.ID); !errors.As(err, &ie) || ie.RequiredMin != 2 || ie.HubVersion != "0.3.0" {
		t.Fatalf("不兼容错误应带详情: %v", err)
	}
	// NotRecorded 不改变错误文字
	if got := NotRecorded(errors.New("原文")).Error(); got != "原文" {
		t.Fatalf("NotRecorded 文字 = %q", got)
	}
	// 没有下发器：不在本机运行，返回失败
	mf.svc.UseRemote(nil)
	if _, err := mf.svc.Run(bg, d.ID); !errors.Is(err, runtime.ErrFailed) {
		t.Fatalf("未接入下发器应返回失败: %v", err)
	}
}

func TestApplyRemoteResult按内容摘要入库(t *testing.T) {
	mf := newMachineFx(t)
	rec := &recNotify{}
	mf.svc.OnChange(rec.add)
	d, err := mf.createOn(devServer, "agentonly", "远程", map[string]any{"host": "a"})
	must(t, err)
	hash := mf.contentHash(t, d.ID)
	before := rec.count(d.ID)

	mf.svc.ApplyRemoteResult(d.ID, hash, &report.Report{Status: report.StatusOK, Summary: "r1", State: "x"}, nil)
	dt, err := mf.svc.Get(bg, d.ID)
	must(t, err)
	if dt.Report == nil || dt.Report.Summary != "r1" {
		t.Fatalf("报告应入库: %+v", dt.Report)
	}
	if _, st := mf.svc.lastFor(d.ID); st != "" {
		t.Fatal("报告的 state 应被清空")
	}
	if rec.count(d.ID) != before+1 || mf.hist.count() != 1 {
		t.Fatalf("应通知一次并写历史: notify=%d hist=%d", rec.count(d.ID)-before, mf.hist.count())
	}

	// 旧内容摘要（下发后配置已改）的结果丢弃
	mf.svc.ApplyRemoteResult(d.ID, "stale-hash", &report.Report{Status: report.StatusOK, Summary: "旧"}, nil)
	dt, _ = mf.svc.Get(bg, d.ID)
	if dt.Report.Summary != "r1" {
		t.Fatal("旧配置的结果不应入库")
	}

	// 失败：保留旧值、计数加一
	mf.svc.ApplyRemoteResult(d.ID, hash, nil, fmt.Errorf("%w: exit 2", runtime.ErrFailed))
	v, _ := mf.svc.View(bg, d.ID)
	if v.Failures != 1 || !v.ReportStale || !strings.Contains(v.LastError, "exit 2") {
		t.Fatalf("失败应保留旧值并计数: %+v", v)
	}

	// 不存在的实例忽略
	mf.svc.ApplyRemoteResult("ghost", hash, &report.Report{Status: report.StatusOK}, nil)
}

func TestDeviceIncompatibleError(t *testing.T) {
	err := fmt.Errorf("包装: %w", &DeviceIncompatibleError{Reason: "version_too_new", RequiredMin: 3, HubVersion: "0.2.0"})
	if !errors.Is(err, ErrDeviceIncompatible) {
		t.Fatal("应可用 errors.Is 判断 ErrDeviceIncompatible")
	}
	if errors.Is(err, ErrDeviceOffline) {
		t.Fatal("不应被判为离线")
	}
}
