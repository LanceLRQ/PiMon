//go:build unix

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

const okReport = `{"status":"ok","summary":"fine","state":"s2"}`

// writeRun 在临时目录写一个 run 脚本并返回路径。
func writeRun(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "run")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func newExec(t *testing.T, body string, timeout time.Duration) *ExecSource {
	t.Helper()
	return NewExecSource(&manifest.Manifest{ID: "t", Timeout: timeout}, writeRun(t, body))
}

func TestExecCollectParsesReport(t *testing.T) {
	src := newExec(t, "cat >/dev/null\nprintf '"+okReport+"'", 5*time.Second)
	rep, err := src.Collect(context.Background(), Input{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusOK || rep.Summary != "fine" || rep.State != "s2" {
		t.Fatalf("报告不对: %+v", rep)
	}
}

func TestExecStdinPayload(t *testing.T) {
	out := filepath.Join(t.TempDir(), "stdin.json")
	src := newExec(t, "cat >"+out+"\nprintf '"+okReport+"'", 5*time.Second)
	px, err := proxy.Parse("socks5h://127.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	last := &report.Report{Status: report.StatusWarning, Summary: "old"}
	_, err = src.Collect(context.Background(), Input{
		Config:  map[string]any{"city": "x"},
		Secrets: map[string]string{"token": "s3cr3t"},
		Proxy:   px,
		Last:    last,
		State:   "st",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("stdin 不是 JSON: %v %s", err, data)
	}
	if got["api_version"] != float64(1) || got["state"] != "st" || got["proxy"] != "socks5h://127.0.0.1:1080" {
		t.Fatalf("stdin 字段不对: %v", got)
	}
	if got["config"].(map[string]any)["city"] != "x" || got["secrets"].(map[string]any)["token"] != "s3cr3t" {
		t.Fatalf("config/secrets 不对: %v", got)
	}
	if got["last"].(map[string]any)["summary"] != "old" {
		t.Fatalf("last 不对: %v", got)
	}
}

func TestExecStdinProxyNullAndEmptyMaps(t *testing.T) {
	out := filepath.Join(t.TempDir(), "stdin.json")
	src := newExec(t, "cat >"+out+"\nprintf '"+okReport+"'", 5*time.Second)
	if _, err := src.Collect(context.Background(), Input{}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	if v, ok := got["proxy"]; !ok || v != nil {
		t.Fatalf("直连时 proxy 应为 null: %v", got)
	}
	if _, ok := got["config"].(map[string]any); !ok {
		t.Fatalf("config 应为空对象: %v", got)
	}
	if _, ok := got["secrets"].(map[string]any); !ok {
		t.Fatalf("secrets 应为空对象: %v", got)
	}
}

func TestExecEnvWhitelistAndSecretsOnlyViaStdin(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "/run/notify")
	t.Setenv("WATCHDOG_USEC", "1000")
	t.Setenv("WATCHDOG_PID", "42")
	t.Setenv("SOME_API_TOKEN", "leak-me")
	t.Setenv("HTTPS_PROXY", "http://inherited:1")
	t.Setenv("LANG", "C")
	out := filepath.Join(t.TempDir(), "env.txt")
	src := newExec(t, "env >"+out+"\necho \"ARGS=$#\" >>"+out+"\ncat >/dev/null\nprintf '"+okReport+"'", 5*time.Second)
	if _, err := src.Collect(context.Background(), Input{Secrets: map[string]string{"token": "zzz-secret-zzz"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	env := string(data)
	for _, bad := range []string{"NOTIFY_SOCKET", "WATCHDOG_USEC", "WATCHDOG_PID", "SOME_API_TOKEN", "leak-me", "zzz-secret-zzz", "inherited:1"} {
		if strings.Contains(env, bad) {
			t.Errorf("环境或参数里不应出现 %q:\n%s", bad, env)
		}
	}
	if !strings.Contains(env, "PATH=") || !strings.Contains(env, "LANG=C") {
		t.Errorf("白名单变量应透传:\n%s", env)
	}
	if !strings.Contains(env, "NO_PROXY=*") {
		t.Errorf("直连应注入 NO_PROXY=*:\n%s", env)
	}
	if !strings.Contains(env, "ARGS=0") {
		t.Errorf("不应有命令行参数:\n%s", env)
	}
}

func TestExecEnvInjectsProxy(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env.txt")
	src := newExec(t, "env >"+out+"\ncat >/dev/null\nprintf '"+okReport+"'", 5*time.Second)
	px, _ := proxy.Parse("socks5h://127.0.0.1:1080")
	if _, err := src.Collect(context.Background(), Input{Proxy: px}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	env := string(data)
	if !strings.Contains(env, "HTTPS_PROXY=socks5h://127.0.0.1:1080") || !strings.Contains(env, "ALL_PROXY=socks5h://127.0.0.1:1080") {
		t.Errorf("应注入代理变量:\n%s", env)
	}
}

func TestExecNonZeroExitIsFailure(t *testing.T) {
	src := newExec(t, "cat >/dev/null\necho boom >&2\nexit 3", 5*time.Second)
	_, err := src.Collect(context.Background(), Input{})
	if !errors.Is(err, ErrFailed) || errors.Is(err, ErrTimeout) {
		t.Fatalf("应为 ErrFailed: %v", err)
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "3") {
		t.Fatalf("错误应带 stderr 与退出码: %v", err)
	}
}

func TestExecInvalidOutputIsFailure(t *testing.T) {
	for name, body := range map[string]string{
		"非JSON":    "cat >/dev/null\necho hello",
		"缺少status": "cat >/dev/null\necho '{\"summary\":\"x\"}'",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newExec(t, body, 5*time.Second).Collect(context.Background(), Input{})
			if !errors.Is(err, ErrFailed) {
				t.Fatalf("应为 ErrFailed: %v", err)
			}
		})
	}
}

func TestExecMissingBinaryIsFailure(t *testing.T) {
	src := NewExecSource(&manifest.Manifest{ID: "t"}, filepath.Join(t.TempDir(), "run"))
	if _, err := src.Collect(context.Background(), Input{}); !errors.Is(err, ErrFailed) {
		t.Fatalf("应为 ErrFailed: %v", err)
	}
}

func TestExecTimeoutKillsProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	src := newExec(t, "cat >/dev/null\nsleep 300 &\necho $! >"+pidFile+"\nwait", 1500*time.Millisecond)
	start := time.Now()
	_, err := src.Collect(context.Background(), Input{})
	if !errors.Is(err, ErrTimeout) || errors.Is(err, ErrFailed) {
		t.Fatalf("应为 ErrTimeout: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("超时后应尽快返回，耗时 %v", time.Since(start))
	}
	data, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("读孙进程 pid: %v", rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("孙进程 %d 超时后仍存活", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestExecContextCancelIsNotTimeout(t *testing.T) {
	src := newExec(t, "cat >/dev/null\nsleep 300", 0)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	_, err := src.Collect(ctx, Input{})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) {
		t.Fatalf("取消应返回 context.Canceled: %v", err)
	}
}

func TestExecStdoutOverLimitIsFailure(t *testing.T) {
	src := newExec(t, "cat >/dev/null\nhead -c 3000000 /dev/zero | tr '\\0' 'a'", 10*time.Second)
	_, err := src.Collect(context.Background(), Input{})
	if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("输出超限应为 ErrFailed: %v", err)
	}
}

func TestExecStderrKeepsOnlyTail(t *testing.T) {
	body := "cat >/dev/null\n" +
		"echo HEAD-MARKER >&2\n" +
		"head -c 100000 /dev/zero | tr '\\0' 'x' >&2\n" +
		"echo TAIL-MARKER >&2\nexit 1"
	_, err := newExec(t, body, 10*time.Second).Collect(context.Background(), Input{})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("应为 ErrFailed: %v", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "HEAD-MARKER") || !strings.Contains(msg, "TAIL-MARKER") {
		t.Fatalf("应只保留 stderr 尾部")
	}
	if len(msg) > MaxStderrBytes+512 {
		t.Fatalf("错误信息过长: %d", len(msg))
	}
}

func TestExecErrorsDoNotLeakSecrets(t *testing.T) {
	body := "cat >/dev/null\necho \"token is zzz-secret-zzz via zzz-proxy-pw\" >&2\nexit 1"
	px, _ := proxy.Parse("http://u:zzz-proxy-pw@127.0.0.1:8080")
	_, err := newExec(t, body, 5*time.Second).Collect(context.Background(), Input{
		Secrets: map[string]string{"token": "zzz-secret-zzz"},
		Proxy:   px,
	})
	if err == nil {
		t.Fatal("应失败")
	}
	if strings.Contains(err.Error(), "zzz-secret-zzz") || strings.Contains(err.Error(), "zzz-proxy-pw") {
		t.Fatalf("错误信息泄露密钥: %v", err)
	}
}

func TestTailBuffer(t *testing.T) {
	b := newTailBuffer(8)
	_, _ = b.Write([]byte("abcde"))
	if b.String() != "abcde" || b.Truncated() {
		t.Fatalf("未超限: %q %v", b.String(), b.Truncated())
	}
	_, _ = b.Write([]byte("fghij"))
	if b.String() != "cdefghij" || !b.Truncated() {
		t.Fatalf("应保留最后 8 字节: %q", b.String())
	}
	_, _ = b.Write([]byte("0123456789ABC"))
	if b.String() != "56789ABC" {
		t.Fatalf("单次写入超过容量: %q", b.String())
	}
}
