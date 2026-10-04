package plugins

import (
	"context"
	"log/slog"
	"os"

	"github.com/LanceLRQ/PiMon/src/internal/pluginscan"
)

// Watch 监视插件目录，变化后防抖 Debounce（默认 1 秒）再重新扫描。
// 防抖计时用注入的 Clock，事件来自真实文件系统。监视建立成功后立即返回；
// 返回的 channel 在监视停止（ctx 结束）后关闭。插件目录不存在时会先创建。
func (r *Registry) Watch(ctx context.Context) (<-chan struct{}, error) {
	if err := os.MkdirAll(r.cfg.Dir, 0o750); err != nil {
		return nil, err
	}
	return pluginscan.Watch(ctx, pluginscan.WatchConfig{
		Dir:      r.cfg.Dir,
		Clock:    r.cfg.Clock,
		Debounce: r.cfg.Debounce,
		Log:      slog.Default(),
	}, func() {
		if _, err := r.Scan(ctx); err != nil && ctx.Err() == nil {
			slog.Error("重新扫描插件目录失败", "err", err)
		}
	})
}
