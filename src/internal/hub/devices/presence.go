package devices

import (
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const (
	// OfflineAfter 是会话断开后转为 offline 所需的时长。
	OfflineAfter = 30 * time.Second
	// StartupGrace 是 hub 启动后给已知设备首次连上的宽限期。
	StartupGrace = 90 * time.Second
)

// presenceEntry 是单个设备的在线状态。gen 在每次状态迁移时全局递增，
// 用来让已登记的旧计时在到期时自动作废。
type presenceEntry struct {
	state string
	since time.Time
	gen   uint64
}

// Presence 在内存中维护设备的原始在线状态（online / disconnected / offline / pending）
// 与状态起点，不读库。dormant 由视图层按「offline 且允许离线」推出，不在此处计算。
// last_seen_at 由会话断开的调用方写库，也不在此处处理。
type Presence struct {
	clk   clock.Clock
	start time.Time

	mu      sync.Mutex
	entries map[string]*presenceEntry
	gen     uint64
	cbs     []func(id string)

	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewPresence 创建在线状态表。known 中的设备初始为 pending，
// start 起满 StartupGrace 仍未连上的改为 offline。
func NewPresence(clk clock.Clock, start time.Time, known []string) *Presence {
	p := &Presence{
		clk: clk, start: start,
		entries: make(map[string]*presenceEntry, len(known)),
		done:    make(chan struct{}),
	}
	for _, id := range known {
		p.gen++
		p.entries[id] = &presenceEntry{state: model.PresencePending, since: start, gen: p.gen}
	}
	// 在构造时就登记等待者，调用方拿到 Presence 后时钟上已有该计时。
	ch := clk.After(start.Add(StartupGrace).Sub(clk.Now()))
	p.wg.Add(1)
	go p.graceLoop(ch)
	return p
}

// OnChange 注册状态变化回调。回调在锁外同步调用，不得阻塞过久。
func (p *Presence) OnChange(fn func(id string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cbs = append(p.cbs, fn)
}

// Connected 标记设备在线。应在 hello 被接受、已发出 hello_ok 之后调用；
// 新配对的设备同样直接调用，不经过 pending。
func (p *Presence) Connected(id string) {
	p.mu.Lock()
	e := p.entries[id]
	if e != nil && e.state == model.PresenceOnline {
		p.mu.Unlock()
		return
	}
	p.set(id, model.PresenceOnline, p.clk.Now())
	p.mu.Unlock()
	p.notify(id)
}

// Disconnected 标记会话在 at 时刻断开，满 OfflineAfter 仍未恢复则转为 offline。
// 设备已处于 disconnected 或 offline 时不重复处理。
func (p *Presence) Disconnected(id string, at time.Time) {
	p.mu.Lock()
	if e := p.entries[id]; e != nil && (e.state == model.PresenceDisconnected || e.state == model.PresenceOffline) {
		p.mu.Unlock()
		return
	}
	gen := p.set(id, model.PresenceDisconnected, at)
	ch := p.clk.After(at.Add(OfflineAfter).Sub(p.clk.Now()))
	p.wg.Add(1)
	p.mu.Unlock()
	p.notify(id)
	go p.offlineLoop(id, gen, at, ch)
}

// State 返回设备当前的原始状态与状态起点；设备未知时状态为空串。
func (p *Presence) State(id string) (state string, since time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[id]
	if e == nil {
		return "", time.Time{}
	}
	return e.state, e.since
}

// Forget 删除设备的状态（吊销时调用），已登记的计时随之失效。
func (p *Presence) Forget(id string) {
	p.mu.Lock()
	_, ok := p.entries[id]
	delete(p.entries, id)
	p.mu.Unlock()
	if ok {
		p.notify(id)
	}
}

// Online 返回当前处于 online 的设备数。
func (p *Presence) Online() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.entries {
		if e.state == model.PresenceOnline {
			n++
		}
	}
	return n
}

// Close 停止全部计时协程并等待其退出，可重复调用。
func (p *Presence) Close() {
	p.closeOnce.Do(func() { close(p.done) })
	p.wg.Wait()
}

// set 写入新状态并返回本次迁移的代数，调用方须持锁。
func (p *Presence) set(id, state string, since time.Time) uint64 {
	p.gen++
	p.entries[id] = &presenceEntry{state: state, since: since, gen: p.gen}
	return p.gen
}

// notify 在锁外依次调用回调。
func (p *Presence) notify(id string) {
	p.mu.Lock()
	cbs := append([]func(string){}, p.cbs...)
	p.mu.Unlock()
	for _, fn := range cbs {
		fn(id)
	}
}

// offlineLoop 等待断开计时到期；设备在此期间重连、再次断开或被 Forget 时，代数不再匹配，不做任何改动。
func (p *Presence) offlineLoop(id string, gen uint64, at time.Time, ch <-chan time.Time) {
	defer p.wg.Done()
	select {
	case <-p.done:
		return
	case <-ch:
	}
	p.mu.Lock()
	e := p.entries[id]
	if e == nil || e.gen != gen || e.state != model.PresenceDisconnected {
		p.mu.Unlock()
		return
	}
	p.set(id, model.PresenceOffline, at.Add(OfflineAfter))
	p.mu.Unlock()
	p.notify(id)
}

// graceLoop 在启动宽限期结束时，把仍为 pending 的设备改为 offline。
func (p *Presence) graceLoop(ch <-chan time.Time) {
	defer p.wg.Done()
	select {
	case <-p.done:
		return
	case <-ch:
	}
	until := p.start.Add(StartupGrace)
	var changed []string
	p.mu.Lock()
	for id, e := range p.entries {
		if e.state == model.PresencePending {
			p.set(id, model.PresenceOffline, until)
			changed = append(changed, id)
		}
	}
	p.mu.Unlock()
	for _, id := range changed {
		p.notify(id)
	}
}
