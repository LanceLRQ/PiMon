package install

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

// TestLaunchd_真实launchctl 用真实 launchctl 走一遍 bootstrap、print、kill SIGTERM（KeepAlive 拉起）、bootout。
// 默认跳过：设置 PIMON_LAUNCHD_TEST=1 才执行（CI 的 macOS runner 用），会在当前用户的 gui 域加载真实 LaunchAgent。
// 为不触碰真实 ~/.pimon，安装的「二进制」是一个 sh 脚本：启动时与收到 SIGTERM 时往 <日志路径>.sig 记一行，
// 由此确认 Restart 发出的是 SIGTERM 且 KeepAlive 重新拉起；家目录用临时目录，测试结束时卸载并清理。
// 这是真实系统上的验收测试，等待 launchd 用真实时间轮询（KeepAlive 节流约 10 秒）。
func TestLaunchd_真实launchctl(t *testing.T) {
	if os.Getenv("PIMON_LAUNCHD_TEST") != "1" {
		t.Skip("设置 PIMON_LAUNCHD_TEST=1 才执行（会加载真实 LaunchAgent）")
	}
	if goruntime.GOOS != "darwin" || os.Geteuid() == 0 {
		t.Skip("只在 macOS 以普通用户运行")
	}
	ctx := context.Background()
	probe := Deps{Runner: sysinstall.ExecRunner{}, EUID: os.Geteuid()}
	if loaded, err := launchdLoaded(ctx, probe); err != nil || loaded {
		t.Skipf("本机已加载 %s 或无法查询（%v），跳过以免干扰", LaunchdLabel, err)
	}
	home := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	script := filepath.Join(t.TempDir(), "fake-agent")
	// ProgramArguments 为 [bin, run, --log-file, <日志>]，$3 即日志路径
	body := "#!/bin/sh\nsig=\"$3.sig\"\necho start >> \"$sig\"\ntrap 'echo TERM >> \"$sig\"; exit 0' TERM\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	d := NewDeps(&out, strings.NewReader(""), false, "darwin", home, "test", script)
	d.Status = nil // 假二进制不会打开控制套接字
	p, err := config.Resolve(config.Env{GOOS: "darwin", EUID: d.EUID, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Uninstall(ctx, d, UninstallOptions{Purge: true}) })

	if err := Install(ctx, d, Options{Listen: fmt.Sprintf("127.0.0.1:%d", port)}); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	if loaded, err := launchdLoaded(ctx, d); err != nil || !loaded {
		t.Fatalf("安装后应已加载: %v\n%s", err, out.String())
	}
	sigFile := p.LogFile + ".sig"
	waitFor := func(what, want string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if b, _ := os.ReadFile(sigFile); string(b) == want {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		b, _ := os.ReadFile(sigFile)
		t.Fatalf("%s：%s 内容为 %q，期望 %q", what, sigFile, b, want)
	}
	waitFor("首次启动", "start\n")
	if err := Restart(ctx, d, p); err != nil {
		t.Fatalf("restart: %v", err)
	}
	waitFor("收到 SIGTERM 后由 KeepAlive 拉起", "start\nTERM\nstart\n")
	// 重跑：plist 不变，不应报错
	out.Reset()
	if err := Install(ctx, d, Options{Listen: fmt.Sprintf("127.0.0.1:%d", port)}); err != nil {
		t.Fatalf("重跑 install: %v\n%s", err, out.String())
	}
	if err := Uninstall(ctx, d, UninstallOptions{Purge: true}); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if loaded, _ := launchdLoaded(ctx, d); loaded {
		t.Fatal("卸载后不应仍加载")
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", LaunchdLabel+".plist")); !os.IsNotExist(err) {
		t.Fatalf("plist 应已删除: %v", err)
	}
}
