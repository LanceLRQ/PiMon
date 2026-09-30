// Package sdnotify 实现 systemd 的 READY 与 WATCHDOG 通知协议。
// 没有 NOTIFY_SOCKET 时所有通知静默忽略，便于在非 systemd 环境运行。
package sdnotify

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
)

// Notifier 按注入的环境变量读取函数工作。
type Notifier struct {
	getenv func(string) string
}

// New 创建 Notifier，getenv 通常传 os.Getenv。
func New(getenv func(string) string) *Notifier { return &Notifier{getenv: getenv} }

// Notify 向 NOTIFY_SOCKET 发送一条状态，例如 "READY=1"。未设置该变量时返回 nil。
func (n *Notifier) Notify(state string) error {
	sock := n.getenv("NOTIFY_SOCKET")
	if sock == "" {
		return nil
	}
	conn, err := net.DialUnix("unixgram", nil, socketAddr(sock))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte(state))
	return err
}

// Ready 通知 systemd 服务已就绪。
func (n *Notifier) Ready() error { return n.Notify("READY=1") }

// WatchdogInterval 返回心跳间隔：WATCHDOG_USEC 的一半。未设置或非法时 ok 为 false。
func (n *Notifier) WatchdogInterval() (time.Duration, bool) {
	usec, err := strconv.ParseInt(n.getenv("WATCHDOG_USEC"), 10, 64)
	if err != nil || usec <= 0 {
		return 0, false
	}
	return time.Duration(usec) * time.Microsecond / 2, true
}

// RunWatchdog 每隔 interval 调用 notify("WATCHDOG=1")，ctx 结束时返回。
// 发送失败不中断循环，下个周期重试。
func RunWatchdog(ctx context.Context, clk clock.Clock, interval time.Duration, notify func(string) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-clk.After(interval):
			_ = notify("WATCHDOG=1")
		}
	}
}

// socketAddr 把 NOTIFY_SOCKET 转为地址，"@" 前缀表示抽象命名空间。
func socketAddr(sock string) *net.UnixAddr {
	if strings.HasPrefix(sock, "@") {
		sock = "\x00" + sock[1:]
	}
	return &net.UnixAddr{Name: sock, Net: "unixgram"}
}
