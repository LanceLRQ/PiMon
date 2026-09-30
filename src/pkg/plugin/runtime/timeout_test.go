package runtime

import (
	"context"
	"errors"
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
