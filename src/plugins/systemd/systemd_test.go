package systemd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cmdexec"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/plugintest"
)

var baseTime = time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newLinux 返回把平台与 systemctl 查找都固定为「可用」的插件，命令由 Fake 回放。
func newLinux(f *cmdexec.Fake) *Plugin {
	p := New(f)
	p.goos = "linux"
	p.lookPath = func(string) (string, error) { return "/usr/bin/systemctl", nil }
	return p
}

func showArgs(units ...string) []string {
	return append([]string{"show", "-p", showProperties, "--"}, units...)
}

func input(units ...any) runtime.Input {
	return runtime.Input{Config: map[string]any{"list": units}, Clock: clock.NewFake(baseTime)}
}

func items(rep *report.Report) map[string]report.Item {
	m := map[string]report.Item{}
	for _, it := range rep.Items {
		m[it.Key] = it
	}
	return m
}

func TestManifest与小组件尺寸(t *testing.T) {
	plugintest.AssertWidgetSizes(t, New(cmdexec.NewFake()).Manifest())
}

func TestCollect多单元各类状态(t *testing.T) {
	f := cmdexec.NewFake()
	units := []string{"ssh.service", "backup.service", "flaky.service", "nginx.service", "ghost.service", "cron.service", "idle.service"}
	f.Set(cmdexec.Result{Stdout: []byte(readTestdata(t, "show-multi.txt"))}, nil, "systemctl", showArgs(units...)...)
	rep, err := newLinux(f).Collect(context.Background(), input("ssh", "backup", "flaky.service", "nginx", "ghost", "cron", "idle"))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, newLinux(f).Manifest(), rep)
	m := items(rep)
	want := map[string]report.Status{
		"unit[ssh.service]":    report.StatusOK,
		"unit[backup.service]": report.StatusOK, // oneshot 已完成
		"unit[flaky.service]":  report.StatusOK,
		"unit[nginx.service]":  report.StatusWarning,
		"unit[ghost.service]":  report.StatusUnknown,
		"unit[cron.service]":   report.StatusCritical,
		"unit[idle.service]":   report.StatusWarning, // 非 oneshot 的 inactive
	}
	for k, st := range want {
		if m[k].State != st {
			t.Errorf("%s 状态 = %s，期望 %s", k, m[k].State, st)
		}
	}
	if m["unit[ghost.service]"].Error == "" || !strings.Contains(m["unit[ghost.service]"].Error, "单元不存在") {
		t.Errorf("not-found 应带条目 error: %+v", m["unit[ghost.service]"])
	}
	if _, ok := m["unit_restarts[ghost.service]"]; ok {
		t.Error("不存在的单元不应有重启次数条目")
	}
	if v := m["unit_restarts[flaky.service]"].Value; v == nil || *v != 3 {
		t.Errorf("flaky 重启次数 = %v，期望 3", v)
	}
	if v := m["unit_restarts[ssh.service]"].Value; v == nil || *v != 0 {
		t.Errorf("ssh 重启次数 = %v，期望 0", v)
	}
	if rep.Status != report.StatusCritical {
		t.Errorf("报告状态 = %s，期望 critical", rep.Status)
	}
	if rep.CollectedAt != baseTime.UnixMilli() {
		t.Errorf("CollectedAt = %d", rep.CollectedAt)
	}
	if !strings.Contains(rep.Summary, "异常 4") {
		t.Errorf("摘要应计入 4 个异常（warning 2、critical 1、not-found 1）: %s", rep.Summary)
	}
}

func TestCollect只有不存在单元时报告至少warning(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{Stdout: []byte("Id=ghost.service\nLoadState=not-found\nActiveState=inactive\nSubState=dead\nResult=success\nNRestarts=\n\n")}, nil, "systemctl", showArgs("ghost.service")...)
	rep, err := newLinux(f).Collect(context.Background(), input("ghost"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusWarning {
		t.Errorf("状态 = %s，期望 warning", rep.Status)
	}
}

func TestCollect省略后缀补service并去重(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{Stdout: []byte(readTestdata(t, "show-old-systemd.txt"))}, nil, "systemctl", showArgs("ssh.service")...)
	_, err := newLinux(f).Collect(context.Background(), input("ssh", " ssh.service ", ""))
	if err != nil {
		t.Fatal(err)
	}
	if c := f.Calls(); len(c) != 1 || c[0].Args[len(c[0].Args)-1] != "ssh.service" {
		t.Errorf("调用 = %+v", c)
	}
}

func TestCollect缺NRestarts为未知而非0(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{Stdout: []byte(readTestdata(t, "show-old-systemd.txt"))}, nil, "systemctl", showArgs("ssh.service")...)
	rep, err := newLinux(f).Collect(context.Background(), input("ssh"))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.AssertValidAgainstManifest(t, newLinux(f).Manifest(), rep)
	r, ok := items(rep)["unit_restarts[ssh.service]"]
	if !ok || r.Value != nil {
		t.Errorf("缺 NRestarts 应给无值数值项: %+v", r)
	}
}

func TestCollect缺ActiveState判unknown并带error(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{Stdout: []byte(readTestdata(t, "show-no-activestate.txt"))}, nil, "systemctl", showArgs("ssh.service")...)
	rep, err := newLinux(f).Collect(context.Background(), input("ssh"))
	if err != nil {
		t.Fatal(err)
	}
	it := items(rep)["unit[ssh.service]"]
	if it.State != report.StatusUnknown || it.Error == "" {
		t.Errorf("条目 = %+v", it)
	}
	if rep.Status == report.StatusOK {
		t.Error("部分失败不应报 ok")
	}
}

func TestCollect屏蔽单元为未知无error(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{Stdout: []byte(readTestdata(t, "show-masked.txt"))}, nil, "systemctl", showArgs("old.service")...)
	rep, err := newLinux(f).Collect(context.Background(), input("old"))
	if err != nil {
		t.Fatal(err)
	}
	it := items(rep)["unit[old.service]"]
	if it.State != report.StatusUnknown || it.Error != "" {
		t.Errorf("条目 = %+v", it)
	}
}

func TestCollect非零Result判critical(t *testing.T) {
	f := cmdexec.NewFake()
	out := "Id=x.service\nLoadState=loaded\nActiveState=inactive\nSubState=dead\nResult=timeout\nType=oneshot\nNRestarts=0\n\n"
	f.Set(cmdexec.Result{Stdout: []byte(out)}, nil, "systemctl", showArgs("x.service")...)
	rep, err := newLinux(f).Collect(context.Background(), input("x"))
	if err != nil {
		t.Fatal(err)
	}
	if st := items(rep)["unit[x.service]"].State; st != report.StatusCritical {
		t.Errorf("状态 = %s", st)
	}
}

func TestCollect非Linux返回不是systemd错误(t *testing.T) {
	p := New(cmdexec.NewFake())
	p.goos = "darwin"
	_, err := p.Collect(context.Background(), input("ssh"))
	if err == nil || !strings.Contains(err.Error(), "此机器不是 systemd 系统") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollect没有systemctl返回不是systemd错误(t *testing.T) {
	p := New(cmdexec.NewFake())
	p.goos = "linux"
	p.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	_, err := p.Collect(context.Background(), input("ssh"))
	if err == nil || !strings.Contains(err.Error(), "此机器不是 systemd 系统") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollect真实路径在非systemd系统返回错误(t *testing.T) {
	// 本机若不是 Linux 或没有 systemctl，真实依赖应走「不是 systemd 系统」分支。
	p := New(cmdexec.NewFake())
	if p.goos == "linux" {
		t.Skip("Linux 上由真实 systemctl 决定")
	}
	if _, err := p.Collect(context.Background(), input("ssh")); err == nil {
		t.Fatal("非 Linux 应返回错误")
	}
}

func TestCollect系统未以systemd启动(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{ExitCode: 1, Stderr: readTestdata(t, "not-booted.stderr")}, nil, "systemctl", showArgs("ssh.service")...)
	_, err := newLinux(f).Collect(context.Background(), input("ssh"))
	if err == nil || !strings.Contains(err.Error(), "此机器不是 systemd 系统") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollect命令非零退出返回错误(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{ExitCode: 1, Stderr: "Invalid unit name"}, nil, "systemctl", showArgs("ssh.service")...)
	_, err := newLinux(f).Collect(context.Background(), input("ssh"))
	if err == nil || !strings.Contains(err.Error(), "Invalid unit name") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollect执行器错误与ctx取消(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{}, errors.New("boom"), "systemctl", showArgs("ssh.service")...)
	if _, err := newLinux(f).Collect(context.Background(), input("ssh")); err == nil {
		t.Fatal("执行器错误应返回 error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.Set(cmdexec.Result{Stdout: []byte(readTestdata(t, "show-old-systemd.txt"))}, nil, "systemctl", showArgs("ssh.service")...)
	if _, err := newLinux(f).Collect(ctx, input("ssh")); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 取消应返回 error: %v", err)
	}
}

func TestCollect段数不符返回错误(t *testing.T) {
	f := cmdexec.NewFake()
	f.Set(cmdexec.Result{Stdout: []byte(readTestdata(t, "show-old-systemd.txt"))}, nil, "systemctl", showArgs("a.service", "b.service")...)
	if _, err := newLinux(f).Collect(context.Background(), input("a", "b")); err == nil {
		t.Fatal("段数不符应返回 error")
	}
}

func TestCollect拒绝选项形态与空列表(t *testing.T) {
	f := cmdexec.NewFake()
	p := newLinux(f)
	for _, bad := range []string{"--host=evil", "a b", "x;y"} {
		if _, err := p.Collect(context.Background(), input(bad)); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
	if _, err := p.Collect(context.Background(), input()); err == nil {
		t.Error("空列表应返回 error")
	}
	if len(f.Calls()) != 0 {
		t.Error("非法配置不应执行命令")
	}
}

func TestSchema校验list的pattern(t *testing.T) {
	m := New(cmdexec.NewFake()).Manifest()
	if errs := schema.Validate(m.ConfigSchema, map[string]any{"list": []any{"ssh", "getty@tty1.service"}}); len(errs) != 0 {
		t.Errorf("合法值被拒绝: %v", errs)
	}
	if errs := schema.Validate(m.ConfigSchema, map[string]any{"list": []any{"a b"}}); len(errs) == 0 {
		t.Error("含空格应被 pattern 拒绝")
	}
	if errs := schema.Validate(m.ConfigSchema, map[string]any{"list": []any{}}); len(errs) == 0 {
		t.Error("空列表应报必填")
	}
}
