package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/events"
	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

// eventErrorBackoff 是发送协程读队列失败后再试的间隔（走 Clock）。
const eventErrorBackoff = 5 * time.Second

// ErrNotRunning 表示守护进程尚未运行或已退出（事件队列不可用）。
var ErrNotRunning = errors.New("agent 守护进程未运行")

// EnqueueEvent 把事件写入持久化队列（供 hook 中继与 ai-tasks 调用）：同 id 已在队列里时静默忽略，
// kind=task 的事件不合法时返回错误；有就绪会话时由发送协程立即发出，否则等下次连接补发。
// Run 之前或退出之后返回 ErrNotRunning。
func (d *Daemon) EnqueueEvent(ctx context.Context, ev events.Event) error {
	d.evMu.RLock()
	defer d.evMu.RUnlock()
	if d.events == nil {
		return ErrNotRunning
	}
	_, err := d.events.Enqueue(ctx, ev)
	return err
}

// onAck 删除被 hub 确认的条目。任何会话（包括被顶替的旧会话）上的 ack 都有效。
// 在会话分发协程中调用，不持有 d.mu。
func (d *Daemon) onAck(m *agent.Ack) {
	d.evMu.RLock()
	defer d.evMu.RUnlock()
	if d.events == nil {
		return
	}
	if err := d.events.Ack(context.Background(), m.IDs); err != nil {
		d.log.Warn("删除已确认的事件失败", "count", len(m.IDs), "err", err)
	}
}

// startSenderLocked 在会话就绪（成为当前会话）时为它起发送协程；调用方持有 d.mu。
func (d *Daemon) startSenderLocked(c *conn) {
	d.evMu.RLock()
	q := d.events
	d.evMu.RUnlock()
	if q == nil {
		return
	}
	d.spawnLocked(func() { d.sendEvents(c, q) })
}

// sendEvents 是一条会话的事件发送协程：先按保留期淘汰，再循环按 seq 升序 Claim 应发送的条目并逐条发送，
// 之后等待入队/ack 唤醒、其他会话让出的在途条目或最早在途条目的重发到期。会话结束或不再是当前会话时退出，
// 退出时释放本会话的在途标记（条目回到未确认，由新会话补发），并唤醒当前会话的发送协程接手。
// 由 spawnLocked 起，Run 退出时 d.wg.Wait 等它结束之后才关闭本地数据库。
func (d *Daemon) sendEvents(c *conn, q *events.Queue) {
	wake, unsubscribe := q.Subscribe()
	defer func() {
		unsubscribe()
		q.Release(c)
		d.kickSender(c)
	}()
	ctx := context.Background()
	if err := q.Prune(ctx); err != nil {
		d.log.Warn("淘汰过期事件失败", "err", err)
	}
	for {
		if !d.isCurrent(c) {
			return
		}
		batch, retry, err := q.Claim(ctx, c)
		if err != nil {
			d.log.Warn("读取事件队列失败，稍后重试", "err", err)
			retry = eventErrorBackoff
		}
		for _, e := range batch {
			if !d.sendEvent(c.s, q, e) {
				return
			}
		}
		var timer <-chan time.Time
		if retry > 0 {
			timer = d.clk.After(retry)
		}
		if d.testHookEventWait != nil {
			d.testHookEventWait(retry)
		}
		select {
		case <-c.s.Done():
			return
		case <-wake:
		case <-c.kick:
		case <-timer:
		}
	}
}

// sendEvent 发送一条事件，会话已断开（或发送队列满而断开）时返回 false。
// 校验失败或超过单帧上限的条目永远发不出去，记 Warn 后从队列删除，免得每 60 秒重试一次并占着窗口。
func (d *Daemon) sendEvent(s *agentlink.Session, q *events.Queue, e events.Entry) bool {
	err := s.Send(&agent.Event{ID: e.ID, Kind: e.Kind, Data: e.Data, At: e.At, Seq: e.Seq})
	var inv *agent.InvalidMessageError
	switch {
	case err == nil:
		return true
	case errors.Is(err, agentlink.ErrFrameTooLarge) || errors.As(err, &inv):
		d.log.Warn("事件无法发送，已从队列删除", "seq", e.Seq, "kind", e.Kind, "err", err)
		if err := q.Ack(context.Background(), []string{e.ID}); err != nil {
			d.log.Warn("删除无法发送的事件失败", "seq", e.Seq, "err", err)
		}
		return true
	default:
		return false
	}
}

// isCurrent 报告 c 是否仍是当前会话（被顶替或已结束的会话停止发送）。
func (d *Daemon) isCurrent(c *conn) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cur == c && !c.ended
}

// kickSender 唤醒当前会话（若不是 from）的发送协程：from 退出时让出的在途条目不会触发队列通知。
func (d *Daemon) kickSender(from *conn) {
	d.mu.Lock()
	cur := d.cur
	d.mu.Unlock()
	if cur == nil || cur == from {
		return
	}
	select {
	case cur.kick <- struct{}{}:
	default:
	}
}
