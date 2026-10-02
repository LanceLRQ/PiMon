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

// minWatchdogInterval 是心跳间隔的下限，避免 WATCHDOG_USEC 过小导致忙循环。
const minWatchdogInterval = time.Second

// Notifier 按注入的环境变量读取函数与进程号读取函数工作。
type Notifier struct {
	getenv func(string) string
	getpid func() int
}

// New 创建 Notifier，getenv 通常传 os.Getenv，getpid 通常传 os.Getpid。
func New(getenv func(string) string, getpid func() int) *Notifier {
	return &Notifier{getenv: getenv, getpid: getpid}
}

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

// WatchdogInterval 返回心跳间隔：WATCHDOG_USEC 的一半，但不小于 1 秒。
// WATCHDOG_USEC 未设置或非法时 ok 为 false；WATCHDOG_PID 非空且不等于本进程号时也为 false，
// 此时看门狗是给别的进程（例如被本进程拉起的子进程继承了环境变量）设置的。
func (n *Notifier) WatchdogInterval() (time.Duration, bool) {
	usec, err := strconv.ParseInt(n.getenv("WATCHDOG_USEC"), 10, 64)
	if err != nil || usec <= 0 {
		return 0, false
	}
	if raw := n.getenv("WATCHDOG_PID"); raw != "" {
		pid, err := strconv.Atoi(raw)
		if err != nil || pid != n.getpid() {
			return 0, false
		}
	}
	return max(time.Duration(usec)*time.Microsecond/2, minWatchdogInterval), true
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
