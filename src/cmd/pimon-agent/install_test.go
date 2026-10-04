package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/agent/install"
)

// installCLI 返回一个把 install/uninstall 的执行替换为记录参数的命令行实例，不触碰真实系统。
func installCLI(t *testing.T, ret error) (*cli, *install.Options, *install.UninstallOptions, *syncBuf) {
	t.Helper()
	out, errb := &syncBuf{}, &syncBuf{}
	c := newCLI(strings.NewReader(""), out, errb, func(k string) string {
		if k == "HOME" {
			return "/home/nobody-test"
		}
		return ""
	})
	var io install.Options
	var uo install.UninstallOptions
	c.doInstall = func(_ context.Context, d install.Deps, o install.Options) error {
		if d.Hello == nil || d.Pair == nil || d.Runner == nil || d.FS == nil {
			t.Fatal("真实依赖未装配完整")
		}
		io = o
		return ret
	}
	c.doUninstall = func(_ context.Context, _ install.Deps, o install.UninstallOptions) error {
		uo = o
		return ret
	}
	return c, &io, &uo, errb
}

func TestInstall_参数映射(t *testing.T) {
	c, io, _, _ := installCLI(t, nil)
	code := c.run([]string{"install", "--hub", "wss://1.2.3.4:31418", "--fp", "SHA256:x", "--code", "C", "--name", "nas", "--re-pair", "--docker-group", "--sudoers", "--user"})
	want := install.Options{User: true, Hub: "wss://1.2.3.4:31418", Fingerprint: "SHA256:x", Code: "C", Name: "nas", RePair: true, DockerGroup: true, Sudoers: true}
	if code != 0 || *io != want {
		t.Fatalf("code=%d opts=%+v", code, *io)
	}
	c, io, _, _ = installCLI(t, nil)
	if code := c.run([]string{"install", "--listen", ":31418"}); code != 0 || io.Listen != ":31418" {
		t.Fatalf("code=%d opts=%+v", code, *io)
	}
}

func TestInstall_退出码(t *testing.T) {
	c, _, _, errb := installCLI(t, install.UsageError("参数不对"))
	if code := c.run([]string{"install"}); code != 2 || !strings.Contains(errb.String(), "参数不对") {
		t.Fatalf("用法错误 code=%d err=%q", code, errb.String())
	}
	c, _, _, _ = installCLI(t, errors.New("失败"))
	if code := c.run([]string{"install", "--listen", ":1"}); code != 1 {
		t.Fatalf("失败 code=%d", code)
	}
	c, _, _, _ = installCLI(t, nil)
	if code := c.run([]string{"install", "--role", "nas"}); code != 2 {
		t.Fatalf("--role 不存在，应为用法错误 code=%d", code)
	}
}

func TestUninstall_参数映射(t *testing.T) {
	c, _, uo, _ := installCLI(t, nil)
	if code := c.run([]string{"uninstall", "--purge", "--user"}); code != 0 || !uo.Purge || !uo.User {
		t.Fatalf("code=%d opts=%+v", code, *uo)
	}
	c, _, _, _ = installCLI(t, nil)
	if code := c.run([]string{"uninstall", "extra"}); code != 2 {
		t.Fatalf("多余参数 code=%d", code)
	}
}

func TestRun_日志写入文件(t *testing.T) {
	e := newEnv(t)
	e.save(dialOutConf())
	logPath := filepath.Join(e.home, "logs", "agent.log")
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	rd := e.startDaemon("--log-file", logPath)
	slog.Info("默认日志探针")
	if code := rd.stop(t); code != 0 {
		t.Fatalf("退出码 %d", code)
	}
	b, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(b), "agent 守护进程启动") {
		t.Fatalf("日志文件内容 %q err=%v", b, err)
	}
	if !strings.Contains(string(b), "默认日志探针") {
		t.Fatalf("slog.Default 的日志应写入日志文件: %q", b)
	}
	if strings.Contains(rd.stderr.String(), "agent 守护进程启动") {
		t.Fatal("指定 --log-file 时不应再写标准错误")
	}
}

func TestInstall_noService参数映射(t *testing.T) {
	c, io, _, _ := installCLI(t, nil)
	if code := c.run([]string{"install", "--listen", ":31418", "--user", "--no-service"}); code != 0 || !io.NoService || !io.User {
		t.Fatalf("code=%d opts=%+v", code, *io)
	}
}
