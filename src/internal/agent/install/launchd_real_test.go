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

	"github.com/LanceLRQ/PiMon/src/internal/agent/config"
	"github.com/LanceLRQ/PiMon/src/internal/sysinstall"
)

// TestLaunchd_真实launchctl 用真实 launchctl 走一遍 bootstrap、print、kickstart、bootout。
// 默认跳过：设置 PIMON_LAUNCHD_TEST=1 才执行（CI 的 macOS runner 用），会在当前用户的 gui 域加载真实 LaunchAgent。
// 为不触碰真实 ~/.pimon，安装的「二进制」是 /usr/bin/true 的副本（立即退出，由 KeepAlive 节流重启），
// 家目录用临时目录；测试结束时卸载并清理。
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

	var out bytes.Buffer
	d := NewDeps(&out, strings.NewReader(""), false, "darwin", home, "test", "/usr/bin/true")
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
	if err := Restart(ctx, d, p); err != nil {
		t.Fatalf("restart: %v", err)
	}
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
