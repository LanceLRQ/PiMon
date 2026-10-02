package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/app"
	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/config"
	"github.com/LanceLRQ/PiMon/src/pkg/version"
)

func runArgs(args ...string) (code int, stdout, stderr string) {
	var o, e bytes.Buffer
	code = run(args, strings.NewReader(""), &o, &e, func(string) string { return "" }, nil)
	return code, o.String(), e.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := runArgs("version")
	if code != 0 || strings.TrimSpace(out) != version.Version {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestHelp(t *testing.T) {
	code, out, _ := runArgs("help")
	if code != 0 || !strings.Contains(out, "serve") || !strings.Contains(out, "restore") || !strings.Contains(out, "plugin") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := runArgs("bogus")
	if code != 2 || !strings.Contains(errOut, "bogus") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if code, _, _ := runArgs(); code != 2 {
		t.Fatalf("无参数应退出 2，得到 %d", code)
	}
}

func TestRestoreNeedsArchive(t *testing.T) {
	code, _, errOut := runArgs("restore", "--data-dir", t.TempDir())
	if code != 1 && code != 2 {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestCommandFailureExitsOne(t *testing.T) {
	code, _, errOut := runArgs("restore", "--data-dir", t.TempDir(), "/nonexistent/x.tar.gz")
	if code != 1 || errOut == "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestPluginHelpAndUsage(t *testing.T) {
	code, out, _ := runArgs("plugin", "help")
	if code != 0 || !strings.Contains(out, "validate") || !strings.Contains(out, "run") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if code, _, _ := runArgs("plugin"); code != 2 {
		t.Fatalf("无子命令应退出 2，得到 %d", code)
	}
	if code, _, _ := runArgs("plugin", "validate"); code != 2 {
		t.Fatalf("缺目录应退出 2，得到 %d", code)
	}
}

func TestPluginValidateExitCodes(t *testing.T) {
	code, out, _ := runArgs("plugin", "validate", "../../../examples/plugins/shell-disk-load")
	if code != 0 || !strings.Contains(out, "校验通过") {
		t.Fatalf("示例应通过: code=%d out=%q", code, out)
	}
	code, _, errOut := runArgs("plugin", "validate", t.TempDir())
	if code != 1 || errOut == "" {
		t.Fatalf("空目录应退出 1: code=%d err=%q", code, errOut)
	}
}

// manifest 错误只应在输出里出现一次（不再先打明细、再打"错误:"）。
func TestPluginValidateErrorPrintedOnce(t *testing.T) {
	dir := t.TempDir()
	yml := "id: x\nversion: 1.0.0\napi_version: 1\nname: x\nkind: sourcee\nruntime: exec\nruns_on: [hub]\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runArgs("plugin", "validate", dir)
	if code != 1 || strings.Count(out+errOut, "sourcee") != 1 || !strings.Contains(errOut, "第 5 行") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func stubRootRun(t *testing.T, err error) *[]string {
	t.Helper()
	var seen []string
	old := checkRootRun
	checkRootRun = func(cfg config.Config) error {
		seen = append(seen, cfg.DataDir)
		return err
	}
	t.Cleanup(func() { checkRootRun = old })
	return &seen
}

func TestRootRefusedForDataCommands(t *testing.T) {
	dir := t.TempDir()
	seen := stubRootRun(t, errors.New("不能以 root 运行"))
	for _, args := range [][]string{
		{"setup-code", "--data-dir", dir},
		{"reset-password", "--data-dir", dir},
		{"restore", "--data-dir", dir, "/nonexistent.tar.gz"},
	} {
		code, _, errOut := runArgs(args...)
		if code != 1 || !strings.Contains(errOut, "不能以 root 运行") {
			t.Errorf("%v: code=%d err=%q", args, code, errOut)
		}
	}
	if len(*seen) != 3 {
		t.Fatalf("三个子命令都应走 root 检查: %v", *seen)
	}
	// 被拒绝时不得创建数据库。
	if _, err := os.Stat(filepath.Join(dir, "pimon.db")); err == nil {
		t.Fatal("拒绝后不应打开或创建数据库")
	}
}

func TestServeSkipsRootCheckAndHoldsDataDirLock(t *testing.T) {
	dir := t.TempDir()
	seen := stubRootRun(t, errors.New("不应被调用"))
	cfg := config.Config{DataDir: dir}
	lk, err := app.LockDataDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lk.Release() }()
	code, _, errOut := runArgs("serve", "--data-dir", dir, "--addr", "127.0.0.1:0")
	if code != 1 || !strings.Contains(errOut, "占用") {
		t.Fatalf("锁被占用时 serve 应失败: code=%d err=%q", code, errOut)
	}
	if len(*seen) != 0 {
		t.Fatal("serve 不应做 root 检查")
	}
	if _, err := os.Stat(filepath.Join(dir, "pimon.db")); err == nil {
		t.Fatal("拿不到锁时不应打开数据库")
	}
}

func TestRestoreRefusedWhileDataDirLocked(t *testing.T) {
	dir := t.TempDir()
	stubRootRun(t, nil)
	lk, err := app.LockDataDir(config.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lk.Release() }()
	code, _, errOut := runArgs("restore", "--data-dir", dir, "/nonexistent.tar.gz")
	if code != 1 || !strings.Contains(errOut, "占用") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestKiosk_缺少图形会话环境时报错退出(t *testing.T) {
	code, _, errOut := runArgs("kiosk")
	if code != 1 || !strings.Contains(errOut, "WAYLAND_DISPLAY") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	code, _, errOut = runArgs("kiosk", "--nope")
	if code != 2 {
		t.Fatalf("未知参数应返回 2: code=%d err=%q", code, errOut)
	}
}

func TestHelp_包含kiosk(t *testing.T) {
	_, out, _ := runArgs("help")
	if !strings.Contains(out, "kiosk") || !strings.Contains(out, "--token-file") {
		t.Fatalf("out=%q", out)
	}
}

func TestSessionWatchdog_参数错误返回2且帮助里有说明(t *testing.T) {
	if code, _, errOut := runArgs("session-watchdog"); code != 2 || !strings.Contains(errOut, "--user") {
		t.Fatalf("缺 --user 应返回 2: code=%d err=%q", code, errOut)
	}
	_, out, _ := runArgs("help")
	if !strings.Contains(out, "session-watchdog") {
		t.Fatalf("帮助缺少 session-watchdog: %q", out)
	}
}

func TestSetupCodeIfNeeded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	stubRootRun(t, nil)
	code1, out1, _ := runArgs("setup-code", "--if-needed", "--data-dir", dir)
	code2, out2, _ := runArgs("setup-code", "--data-dir", dir, "--if-needed")
	if code1 != 0 || code2 != 0 {
		t.Fatalf("code=%d,%d", code1, code2)
	}
	first := strings.SplitN(out1, "\n", 2)[0]
	if !strings.HasPrefix(first, "首次设置码: ") || strings.SplitN(out2, "\n", 2)[0] != first {
		t.Fatalf("第二次应打印同一个码: %q / %q", out1, out2)
	}
	// 不带 --if-needed 仍然生成新码。
	_, out3, _ := runArgs("setup-code", "--data-dir", dir)
	if strings.SplitN(out3, "\n", 2)[0] == first {
		t.Fatalf("不带 --if-needed 应换新码: %q", out3)
	}
}

func TestSetupCodeIfNeededAdminExistsExitsThree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	stubRootRun(t, nil)
	cfg := config.Config{Addr: "127.0.0.1:0", DataDir: dir, LogLevel: "info"}
	a, err := app.Open(context.Background(), cfg, app.WithHasherParams(auth.Params{Memory: 64, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}), app.WithStderr(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := a.SetupCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	body, _ := json.Marshal(map[string]any{"setup_code": code, "password": "correct horse", "language": "zh", "timezone": "UTC", "access_url": ""})
	req, _ := http.NewRequest("POST", srv.URL+"/api/setup", bytes.NewReader(body))
	req.Header.Set("Origin", srv.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("setup: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	srv.Close()
	_ = a.Close()

	rc, out, errOut := runArgs("setup-code", "--if-needed", "--data-dir", dir)
	if rc != 3 || !strings.Contains(out, "已设置管理员") || strings.Contains(errOut, "错误") {
		t.Fatalf("rc=%d out=%q err=%q", rc, out, errOut)
	}
	// 不带 --if-needed 时保持原行为：失败退出 1。
	if rc, _, _ := runArgs("setup-code", "--data-dir", dir); rc != 1 {
		t.Fatalf("不带 --if-needed 应退出 1，得到 %d", rc)
	}
}

// 只测参数解析与帮助；绝不走到真实安装（CI 以 root 运行时会真的改系统）。
func TestInstallUsage(t *testing.T) {
	if code, out, _ := runArgs("help"); code != 0 || !strings.Contains(out, "install") || !strings.Contains(out, "--desktop-user") {
		t.Fatalf("帮助应列出 install: %q", out)
	}
	if code, _, errOut := runArgs("install", "extra"); code != 2 || !strings.Contains(errOut, "位置参数") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if code, _, _ := runArgs("install", "--bogus"); code != 2 {
		t.Fatalf("未知参数应退出 2，得到 %d", code)
	}
}
