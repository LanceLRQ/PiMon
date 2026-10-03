package cmdexec

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCapturesStdoutAndExitCode(t *testing.T) {
	res, err := New().Run(context.Background(), "sh", "-c", "printf hello; echo oops >&2; exit 3")
	if err != nil {
		t.Fatalf("非 0 退出不应返回 error: %v", err)
	}
	if string(res.Stdout) != "hello" || res.ExitCode != 3 || !strings.Contains(res.Stderr, "oops") {
		t.Fatalf("结果不符: %+v", res)
	}
}

func TestRunSetsLocale(t *testing.T) {
	res, err := New().Run(context.Background(), "sh", "-c", "printf %s \"$LC_ALL\"")
	if err != nil || string(res.Stdout) != "C" {
		t.Fatalf("LC_ALL 应为 C: %q %v", res.Stdout, err)
	}
}

func TestRunStartFailure(t *testing.T) {
	if _, err := New().Run(context.Background(), "/nonexistent/pimon-cmd"); err == nil {
		t.Fatal("启动失败应返回 error")
	}
}

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	// 孙进程持有 stdout 管道，若只杀主进程会一直等到 sleep 结束。
	_, err := New().Run(ctx, "sh", "-c", "sleep 30 & sleep 30")
	if err == nil {
		t.Fatal("超时应返回 error")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("超时后未及时返回: %v", time.Since(start))
	}
}

func TestRunStdoutOverLimit(t *testing.T) {
	_, err := New().Run(context.Background(), "sh", "-c", "yes | head -c 2000000")
	if err == nil {
		t.Fatal("stdout 超过 1 MiB 应返回 error")
	}
}

func TestRunStderrKeepsTail(t *testing.T) {
	res, err := New().Run(context.Background(), "sh", "-c", "head -c 40000 /dev/zero | tr '\\0' a >&2; printf END >&2")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stderr) != MaxStderrBytes || !strings.HasSuffix(res.Stderr, "END") {
		t.Fatalf("stderr 应保留尾部 %d 字节: len=%d", MaxStderrBytes, len(res.Stderr))
	}
}

func TestFakeRecordsCallsAndReturnsPreset(t *testing.T) {
	f := NewFake()
	f.Set(Result{Stdout: []byte("x"), ExitCode: 2}, nil, "smartctl", "--scan-open")
	res, err := f.Run(context.Background(), "smartctl", "--scan-open")
	if err != nil || string(res.Stdout) != "x" || res.ExitCode != 2 {
		t.Fatalf("预置结果不符: %+v %v", res, err)
	}
	if _, err := f.Run(context.Background(), "mdadm", "--detail"); err == nil {
		t.Fatal("未预置的命令应返回 error")
	}
	calls := f.Calls()
	if len(calls) != 2 || calls[0].Name != "smartctl" || calls[1].Args[0] != "--detail" {
		t.Fatalf("调用记录不符: %+v", calls)
	}
}

func TestFakeReturnsPresetError(t *testing.T) {
	f := NewFake()
	f.Set(Result{}, context.DeadlineExceeded, "docker")
	if _, err := f.Run(context.Background(), "docker"); err != context.DeadlineExceeded {
		t.Fatalf("应返回预置错误: %v", err)
	}
}
