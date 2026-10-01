package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

type funcSource struct {
	m       *manifest.Manifest
	collect func(ctx context.Context, in Input) (*report.Report, error)
}

func (f funcSource) Manifest() *manifest.Manifest { return f.m }
func (f funcSource) Collect(ctx context.Context, in Input) (*report.Report, error) {
	return f.collect(ctx, in)
}

func TestCollectWithTimeoutBuiltinTimesOut(t *testing.T) {
	src := funcSource{
		m: &manifest.Manifest{ID: "b", Timeout: 50 * time.Millisecond},
		collect: func(ctx context.Context, _ Input) (*report.Report, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	_, err := CollectWithTimeout(context.Background(), src, Input{})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("应为 ErrTimeout: %v", err)
	}
}

func TestCollectWithTimeoutWrapsOtherErrors(t *testing.T) {
	cause := errors.New("网络不通")
	src := funcSource{
		m: &manifest.Manifest{ID: "b", Timeout: time.Second},
		collect: func(context.Context, Input) (*report.Report, error) {
			return nil, cause
		},
	}
	_, err := CollectWithTimeout(context.Background(), src, Input{})
	if !errors.Is(err, ErrFailed) || !errors.Is(err, cause) || errors.Is(err, ErrTimeout) {
		t.Fatalf("应为包装了原因的 ErrFailed: %v", err)
	}
}

func TestCollectWithTimeoutKeepsCancelAndSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := funcSource{
		m: &manifest.Manifest{ID: "b", Timeout: time.Second},
		collect: func(ctx context.Context, _ Input) (*report.Report, error) {
			return nil, ctx.Err()
		},
	}
	if _, err := CollectWithTimeout(ctx, src, Input{}); !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) {
		t.Fatalf("取消应原样返回: %v", err)
	}
	ok := funcSource{
		m: &manifest.Manifest{ID: "b"},
		collect: func(context.Context, Input) (*report.Report, error) {
			return &report.Report{Status: report.StatusOK}, nil
		},
	}
	rep, err := CollectWithTimeout(context.Background(), ok, Input{})
	if err != nil || rep.Status != report.StatusOK {
		t.Fatalf("成功路径: %v %v", rep, err)
	}
}

const leakURL = "https://api.example.com/v1?key=zzz-secret-key-zzz"

func leakySource() funcSource {
	return funcSource{
		m: &manifest.Manifest{ID: "b", Timeout: time.Second},
		collect: func(context.Context, Input) (*report.Report, error) {
			return nil, &url.Error{Op: "Get", URL: leakURL, Err: errors.New("connection refused")}
		},
	}
}

func TestCollectWithTimeoutRedactsSecretsInBuiltinErrors(t *testing.T) {
	in := Input{Secrets: map[string]string{"endpoint": leakURL, "key": "zzz-secret-key-zzz"}}
	_, err := CollectWithTimeout(context.Background(), leakySource(), in)
	if err == nil || !errors.Is(err, ErrFailed) || errors.Is(err, ErrTimeout) {
		t.Fatalf("应仍为 ErrFailed: %v", err)
	}
	if strings.Contains(err.Error(), "zzz-secret-key-zzz") {
		t.Fatalf("builtin 错误泄露密钥: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("应保留非敏感的原因: %v", err)
	}
}

func TestCollectWithTimeoutRedactsTimeoutErrors(t *testing.T) {
	src := funcSource{
		m: &manifest.Manifest{ID: "b", Timeout: 20 * time.Millisecond},
		collect: func(ctx context.Context, _ Input) (*report.Report, error) {
			<-ctx.Done()
			return nil, fmt.Errorf("请求 %s 超时: %w", leakURL, ctx.Err())
		},
	}
	_, err := CollectWithTimeout(context.Background(), src, Input{Secrets: map[string]string{"key": "zzz-secret-key-zzz"}})
	if !errors.Is(err, ErrTimeout) || strings.Contains(err.Error(), "zzz-secret-key-zzz") {
		t.Fatalf("超时应分类正确且脱敏: %v", err)
	}
}

func TestSchedulerLogsDoNotLeakBuiltinSecrets(t *testing.T) {
	var buf syncBuffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	in := Input{Secrets: map[string]string{"key": "zzz-secret-key-zzz"}}
	h := newSchedHarness(t, SchedulerOptions{Logger: log})
	h.s.Upsert(Task{ID: "a", Interval: time.Hour, ConfigHash: "h", Run: func(ctx context.Context) (*report.Report, error) {
		return CollectWithTimeout(ctx, leakySource(), in)
	}})
	r := h.next(t)
	if !errors.Is(r.Err, ErrFailed) {
		t.Fatalf("应为 ErrFailed: %v", r.Err)
	}
	if out := buf.String(); out == "" || strings.Contains(out, "zzz-secret-key-zzz") {
		t.Fatalf("日志应有记录且不含密钥:\n%s", out)
	}
	if strings.Contains(r.Err.Error(), "zzz-secret-key-zzz") {
		t.Fatalf("结果错误泄露密钥: %v", r.Err)
	}
}

// syncBuffer 是并发安全的日志缓冲。
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
