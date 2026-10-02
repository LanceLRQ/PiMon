package app

import (
	"context"
	"log/slog"
	"sync"
)

// machineNotifier 把「设备状态变化」异步转成「该设备上全部实例的变化通知」。
// 设备仓库与在线状态的回调可能在 agentsrv 的会话流程或数据库事务附近同步触发，
// 而查询实例要读库（单连接），所以回调里只登记设备 ID，由独立协程合并后处理。
type machineNotifier struct {
	run    func(ctx context.Context, machine string) error
	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	pending map[string]struct{}
	closed  bool
}

func newMachineNotifier(run func(ctx context.Context, machine string) error) *machineNotifier {
	ctx, cancel := context.WithCancel(context.Background())
	n := &machineNotifier{
		run: run, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), pending: map[string]struct{}{},
	}
	n.wg.Add(1)
	go n.loop()
	return n
}

// Submit 登记一个设备，非阻塞；关闭后忽略。
func (n *machineNotifier) Submit(machine string) {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.pending[machine] = struct{}{}
	n.mu.Unlock()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

func (n *machineNotifier) take() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.pending))
	for m := range n.pending {
		out = append(out, m)
	}
	clear(n.pending)
	return out
}

func (n *machineNotifier) loop() {
	defer n.wg.Done()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-n.wake:
		}
		for _, m := range n.take() {
			if err := n.run(n.ctx, m); err != nil && n.ctx.Err() == nil {
				slog.Warn("通知设备上的实例失败", "device_id", m, "err", err)
			}
		}
	}
}

// Close 停止处理并等待协程退出，须在关闭数据库之前调用；可重复调用。
func (n *machineNotifier) Close() {
	n.mu.Lock()
	n.closed = true
	n.mu.Unlock()
	n.cancel()
	n.wg.Wait()
}
