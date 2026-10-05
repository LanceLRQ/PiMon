// Package events 是 agent 的持久化事件队列：入队时分配单调 seq，按条数与入队时间有界淘汰，
// 并为发送协程提供「取未确认且不在途」的条目、发送窗口与超时重发的计时。
// 存储细节（SQL、事务）在 internal/agent/store，本包只管队列语义。
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/agent/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// 队列默认参数。
const (
	DefaultMaxEntries  = 10000
	DefaultMaxAge      = 7 * 24 * time.Hour
	DefaultWindow      = 128
	DefaultResendAfter = 60 * time.Second

	idMaxBytes = 128
	pageSize   = 256
)

// Event 是调用方入队的事件。ID 由调用方给出（同 ID 已在队列里时静默忽略），seq 由队列分配。
type Event struct {
	ID   string
	Kind string
	Data json.RawMessage
	At   time.Time
}

// Entry 是队列里已分配 seq 的条目。
type Entry = store.QueuedEvent

// Stats 是队列当前状态。
type Stats = store.EventStats

// Option 调整队列参数。
type Option func(*Queue)

// WithMaxEntries 设置条数上限。
func WithMaxEntries(n int) Option { return func(q *Queue) { q.maxEntries = n } }

// WithMaxAge 设置按入队时间计的保留期。
func WithMaxAge(d time.Duration) Option { return func(q *Queue) { q.maxAge = d } }

// WithWindow 设置单个会话的发送窗口（已发未确认条数上限）。
func WithWindow(n int) Option { return func(q *Queue) { q.window = n } }

// WithResendAfter 设置在途条目超过该时长未确认即重发。
func WithResendAfter(d time.Duration) Option { return func(q *Queue) { q.resendAfter = d } }

type inflight struct {
	id     string
	owner  any
	sentAt time.Time
}

// Queue 是持久化事件队列，并发安全。
type Queue struct {
	st  *store.Store
	clk clock.Clock

	maxEntries  int
	maxAge      time.Duration
	window      int
	resendAfter time.Duration

	mu     sync.Mutex
	flight map[int64]inflight // seq -> 在途信息；只在内存里，重启后全部视为未发送
	subs   map[chan struct{}]struct{}
}

// New 基于已打开的 agent store 创建队列；clk 用于在途重发计时（入队时间用 store 自己的时钟）。
func New(st *store.Store, clk clock.Clock, opts ...Option) *Queue {
	q := &Queue{
		st: st, clk: clk,
		maxEntries: DefaultMaxEntries, maxAge: DefaultMaxAge,
		window: DefaultWindow, resendAfter: DefaultResendAfter,
		flight: map[int64]inflight{}, subs: map[chan struct{}]struct{}{},
	}
	for _, o := range opts {
		o(q)
	}
	return q
}

// Enqueue 校验并入队，返回分配的 seq；同 ID 已在队列里时返回 0 且不报错。
// kind=task 的事件先经 model.TaskEvent.Normalize，不合法直接返回错误，入库的是规范化后的数据。
func (q *Queue) Enqueue(ctx context.Context, e Event) (int64, error) {
	if e.ID == "" || len(e.ID) > idMaxBytes {
		return 0, errors.New("事件 id 为空或过长")
	}
	if e.Kind == "" {
		return 0, errors.New("事件 kind 不能为空")
	}
	if e.At.IsZero() {
		return 0, errors.New("事件 at 不能为零")
	}
	if !json.Valid(e.Data) {
		return 0, errors.New("事件 data 不是合法 JSON")
	}
	data := e.Data
	if e.Kind == model.EventKindTask {
		var te model.TaskEvent
		if err := json.Unmarshal(e.Data, &te); err != nil {
			return 0, fmt.Errorf("任务事件解析: %w", err)
		}
		if err := te.Normalize(); err != nil {
			return 0, fmt.Errorf("任务事件校验: %w", err)
		}
		b, err := json.Marshal(te)
		if err != nil {
			return 0, fmt.Errorf("任务事件序列化: %w", err)
		}
		data = b
	}
	seq, inserted, _, err := q.st.AppendEvent(ctx, store.QueuedEvent{ID: e.ID, Kind: e.Kind, Data: data, At: e.At}, q.maxEntries, q.maxAge)
	if err != nil {
		return 0, err
	}
	if inserted {
		q.notify()
	}
	return seq, nil
}

// Prune 立即按保留期与条数上限淘汰（会话就绪时调用）。
func (q *Queue) Prune(ctx context.Context) error {
	_, err := q.st.PruneEvents(ctx, q.maxEntries, q.maxAge)
	return err
}

// Pending 按 seq 升序返回 seq > afterSeq 的未确认条目（不区分是否在途）。
func (q *Queue) Pending(ctx context.Context, afterSeq int64, limit int) ([]Entry, error) {
	return q.st.PendingEvents(ctx, afterSeq, limit)
}

// Claim 供发送协程使用：按 seq 升序取出该 owner 应当发送的条目并标记为在途。
// 取出的包括从未发送的新条目（受发送窗口限制：owner 的在途数不超过窗口）、
// 在途超过 resendAfter 未确认的条目、以及其他 owner 遗留的在途条目。
// nextRetry 是该 owner 最早一条在途条目到期重发的剩余时间，没有在途条目时为 0；
// 发送协程应同时等待 Subscribe 的唤醒与该时长的计时。
func (q *Queue) Claim(ctx context.Context, owner any) (out []Entry, nextRetry time.Duration, err error) {
	// 先清掉已被淘汰（不在库里）的在途标记，避免占着窗口。
	head, err := q.st.PendingEvents(ctx, 0, 1)
	if err != nil {
		return nil, 0, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clk.Now()
	if len(head) == 0 {
		clear(q.flight)
		return nil, 0, nil
	}
	for seq := range q.flight {
		if seq < head[0].Seq {
			delete(q.flight, seq)
		}
	}
	used := 0
	for _, f := range q.flight {
		if f.owner == owner {
			used++
		}
	}
	var after int64
	for {
		page, err := q.st.PendingEvents(ctx, after, pageSize)
		if err != nil {
			return nil, 0, err
		}
		for _, e := range page {
			f, ok := q.flight[e.Seq]
			switch {
			case !ok:
				if used >= q.window {
					continue
				}
				used++
			case f.owner == owner && now.Sub(f.sentAt) < q.resendAfter:
				continue
			}
			q.flight[e.Seq] = inflight{id: e.ID, owner: owner, sentAt: now}
			out = append(out, e)
		}
		if len(page) < pageSize {
			break
		}
		after = page[len(page)-1].Seq
	}
	for _, f := range q.flight {
		if f.owner != owner {
			continue
		}
		if left := q.resendAfter - now.Sub(f.sentAt); nextRetry == 0 || left < nextRetry {
			nextRetry = left
		}
	}
	return out, nextRetry, nil
}

// Release 丢弃 owner 的全部在途标记（会话结束时调用），其条目回到「未确认且不在途」，由新会话补发。
func (q *Queue) Release(owner any) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for seq, f := range q.flight {
		if f.owner == owner {
			delete(q.flight, seq)
		}
	}
}

// Ack 删除已被 hub 确认的条目；重复确认与不存在的 id 无副作用。
func (q *Queue) Ack(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := q.st.DeleteEvents(ctx, ids); err != nil {
		return err
	}
	acked := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		acked[id] = struct{}{}
	}
	q.mu.Lock()
	for seq, f := range q.flight {
		if _, ok := acked[f.id]; ok {
			delete(q.flight, seq)
		}
	}
	q.mu.Unlock()
	q.notify() // 窗口腾出了空位
	return nil
}

// Dropped 返回因淘汰丢弃的累计条数（持久化，重启不归零）。
func (q *Queue) Dropped(ctx context.Context) (int64, error) {
	st, err := q.st.EventStats(ctx)
	return st.Dropped, err
}

// Stats 返回队列条数、累计丢弃数与已分配的最大 seq。
func (q *Queue) Stats(ctx context.Context) (Stats, error) { return q.st.EventStats(ctx) }

// Subscribe 返回一个唤醒通道（容量 1，合并连续通知）：入队了新条目或 ack 腾出窗口时可读。
// 调用 cancel 取消订阅。
func (q *Queue) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	q.mu.Lock()
	q.subs[ch] = struct{}{}
	q.mu.Unlock()
	return ch, func() {
		q.mu.Lock()
		delete(q.subs, ch)
		q.mu.Unlock()
	}
}

func (q *Queue) notify() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for ch := range q.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
