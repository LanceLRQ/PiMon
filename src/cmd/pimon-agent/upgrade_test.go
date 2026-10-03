package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/agent/ctl"
	"github.com/LanceLRQ/PiMon/src/internal/agent/install"
	"github.com/LanceLRQ/PiMon/src/internal/agent/upgrade"
)

// upgradeCLI 返回一个把升级执行替换为记录参数的命令行实例，不下载、不替换任何二进制。
func upgradeCLI(t *testing.T, ret error) (*cli, *upgrade.Options, *upgrade.Deps, *syncBuf) {
	t.Helper()
	out, errb := &syncBuf{}, &syncBuf{}
	c := newCLI(strings.NewReader(""), out, errb, func(k string) string {
		if k == "HOME" {
			return "/home/nobody-test"
		}
		return ""
	})
	var o upgrade.Options
	var d upgrade.Deps
	c.doUpgrade = func(_ context.Context, dd upgrade.Deps, oo upgrade.Options) error {
		o, d = oo, dd
		return ret
	}
	return c, &o, &d, errb
}

func TestUpgrade_参数映射(t *testing.T) {
	c, o, d, _ := upgradeCLI(t, nil)
	if code := c.run([]string{"upgrade", "--release", "v0.2.0", "--proxy", "socks5://127.0.0.1:1080", "--user"}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if *o != (upgrade.Options{Release: "v0.2.0", Proxy: "socks5://127.0.0.1:1080"}) {
		t.Fatalf("opts=%+v", *o)
	}
	if d.UpgradeInfo == nil || d.LoadConfig == nil || d.Runner == nil || d.Out == nil || d.ReleaseBase != upgrade.DefaultReleaseBase {
		t.Fatal("真实依赖未装配完整")
	}
	if c.goos == "linux" && d.Paths.System {
		t.Fatal("--user 应解析为用户模式路径")
	}
}

func TestUpgrade_退出码(t *testing.T) {
	c, _, _, errb := upgradeCLI(t, install.UsageError("--release 格式不对"))
	if code := c.run([]string{"upgrade", "--release", "x"}); code != 2 || !strings.Contains(errb.String(), "格式不对") {
		t.Fatalf("用法错误 code=%d err=%q", code, errb.String())
	}
	c, _, _, errb = upgradeCLI(t, errors.New("校验和不匹配"))
	if code := c.run([]string{"upgrade"}); code != 1 || !strings.Contains(errb.String(), "校验和不匹配") {
		t.Fatalf("失败 code=%d err=%q", code, errb.String())
	}
	c, _, _, errb = upgradeCLI(t, ctl.ErrNotRunning)
	if code := c.run([]string{"upgrade"}); code != 1 || !strings.Contains(errb.String(), "守护进程未运行") {
		t.Fatalf("未运行 code=%d err=%q", code, errb.String())
	}
	c, _, _, _ = upgradeCLI(t, nil)
	if code := c.run([]string{"upgrade", "extra"}); code != 2 {
		t.Fatalf("多余参数 code=%d", code)
	}
}

// 守护进程在运行但未连上 hub 时，upgrade 经真实控制套接字拿到「未连接 hub」。
func TestUpgrade_未连接hub提示(t *testing.T) {
	e := newEnv(t)
	conf, _ := dialInConf(t)
	e.save(conf)
	if err := os.MkdirAll(filepath.Dir(e.paths.Bin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.paths.Bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.startDaemon()
	code, _, errb := e.call("upgrade", "--user")
	if code != 1 || !strings.Contains(errb, "未连接 hub") {
		t.Fatalf("code=%d err=%q", code, errb)
	}
	if b, _ := os.ReadFile(e.paths.Bin); string(b) != "old" {
		t.Fatal("二进制被改动")
	}
}
