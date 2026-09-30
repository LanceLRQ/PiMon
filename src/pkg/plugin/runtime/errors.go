package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// 采集失败的分类哨兵，调用方用 errors.Is 判断：
// ErrTimeout 对应 API 错误码 run.timeout，ErrFailed 对应 run.failed。
var (
	// ErrTimeout 表示采集超过 manifest 声明的超时时间。
	ErrTimeout = errors.New("采集超时")
	// ErrFailed 表示采集失败：退出码非 0、输出不合法、输出超限、网络错误等。
	ErrFailed = errors.New("采集失败")
)

// classify 把任意采集错误归入 ErrTimeout / ErrFailed。
// parent 是调用方传入的上下文：它被取消（非超时）时错误原样返回，不算采集失败。
func classify(parent, runCtx context.Context, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrTimeout), errors.Is(err, ErrFailed):
		return err
	case errors.Is(runCtx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	case parent.Err() != nil && errors.Is(err, context.Canceled):
		return err
	default:
		return fmt.Errorf("%w: %w", ErrFailed, err)
	}
}

// CollectWithTimeout 按 manifest.Timeout 给一次采集加超时，并把错误归类为
// ErrTimeout / ErrFailed（调用方取消时原样返回 context.Canceled）。
// 内置与 exec 两种形态共用，内置插件同样受 ctx 超时控制。
// 返回的错误已脱敏：Input 里的密钥值与代理凭据不会出现在错误文字里（内置插件的
// *url.Error 等常带完整 URL）。
func CollectWithTimeout(ctx context.Context, src Source, in Input) (*report.Report, error) {
	runCtx := ctx
	if d := src.Manifest().Timeout; d > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	rep, err := src.Collect(runCtx, in)
	if err != nil {
		return nil, redactError(classify(ctx, runCtx, err), in)
	}
	return rep, nil
}

// backoffCap 是连续失败时间隔放大的上限倍数。
const backoffCap = 10

// BackoffInterval 返回连续失败 failures 次后的下次运行间隔：
// 每失败一次翻倍，上限为 10 倍基础间隔；failures <= 0 时即基础间隔。
func BackoffInterval(base time.Duration, failures int) time.Duration {
	factor := 1
	for i := 0; i < failures && factor < backoffCap; i++ {
		factor *= 2
	}
	if factor > backoffCap {
		factor = backoffCap
	}
	return base * time.Duration(factor)
}
