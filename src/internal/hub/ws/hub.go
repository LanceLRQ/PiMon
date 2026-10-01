package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

const (
	// flushInterval 是变化合并窗口：同一实例的状态变化最快每秒推送一次。
	flushInterval = time.Second
	// idleTimeout 是客户端无任何消息的最长时间，超过即断开。
	idleTimeout = 60 * time.Second
	// reconcileInterval 是对账周期：展示状态随时间自行变化（如过期）时没有变更通知，
	// 靠定期对账把变化推出去。
	reconcileInterval = 15 * time.Second
	// sessionRecheckInterval 是已建立连接复核会话（登出、吊销、过期）的周期。
	sessionRecheckInterval = 15 * time.Second
	// defaultQueueSize 是每连接发送队列的默认容量。
	defaultQueueSize = 64
)

// InstanceSource 是实例状态的来源，由 instances.Service 实现。
// View 在实例不存在时返回 instances.ErrNotFound。
type InstanceSource interface {
	List(ctx context.Context) ([]model.Instance, error)
	View(ctx context.Context, id string) (model.Instance, error)
}

// SettingsSource 是全局设置的来源，由 settings.Service 实现。
type SettingsSource interface {
	Get() model.Settings
}

// SessionLookup 按会话令牌识别会话，由 auth.Sessions 实现。
type SessionLookup interface {
	Lookup(ctx context.Context, token string) (auth.SessionKind, bool, error)
}

// Config 是 Hub 的依赖。
type Config struct {
	Clock clock.Clock
	// Build 是构建版本，随 snapshot 下发，须与注入 index.html 的值同源。
	Build     string
	Instances InstanceSource
	Settings  SettingsSource
	Sessions  SessionLookup
	// 以下四项接入屏幕相关主题；为空时对应主题不下发内容。
	Layouts     LayoutSource
	ScreenState ScreenStateSource
	ScreenData  ScreenDataSource
	// Sink 接收屏幕会话的上报与在线状态。
	Sink ScreenSink
	// QueueSize 是每连接发送队列容量，缺省 64。
	QueueSize int

	// afterFlush 仅供测试：每次合并窗口的 flush 执行完毕后调用，
	// 让测试在假时钟推进后确定性地等待异步 flush 结束。
	afterFlush func()
}

var errClosed = errors.New("hub 已关闭")

// Hub 是广播中心。所有方法可并发使用。
type Hub struct {
	cfg Config
	clk clock.Clock

	// bmu 串行化「构建 snapshot 并登记连接」与「构建 patch 并入队」，
	// 保证新连接的 snapshot 之后不会收到比 snapshot 更旧的 patch。
	// 持有 bmu 时可以读数据库，但不得调用会回调 NotifyInstance 的写操作。
	bmu     sync.Mutex
	conns   map[*client]struct{}
	last    map[string]model.Instance // 最近一次广播给客户端的实例状态，对账与去重用
	changed chan struct{}             // 连接集合变化时关闭并换新，测试等待用
	closed  bool

	// 屏幕相关状态，只在持有 bmu 时读写。
	layoutVer    int                                 // 最近一次广播给管理员的布局版本，丢弃较旧的回调
	lastResolved []byte                              // 最近一次广播给屏幕的解析后布局（JSON）
	lastData     map[string]model.ScreenInstanceData // 最近一次广播的引用实例数据
	screenConns  int                                 // 在线的屏幕会话数

	// mu 保护待推送集合，只做内存操作，供业务路径上的回调调用。
	mu           sync.Mutex
	pendInst     map[string]struct{}
	pendSettings bool
	armed        bool

	stop     chan struct{}
	stopOnce sync.Once
}

// New 创建 Hub。
func New(c Config) *Hub {
	if c.Clock == nil {
		c.Clock = clock.Real{}
	}
	if c.QueueSize <= 0 {
		c.QueueSize = defaultQueueSize
	}
	return &Hub{
		cfg: c, clk: c.Clock,
		conns: map[*client]struct{}{}, changed: make(chan struct{}),
		pendInst: map[string]struct{}{}, stop: make(chan struct{}),
	}
}

// NotifyInstance 登记一个实例发生了变化（新建、修改、删除、采集结果等），
// 约一秒后合并推送给订阅者。非阻塞，可在业务路径上同步调用。
func (h *Hub) NotifyInstance(id string) {
	h.mu.Lock()
	h.pendInst[id] = struct{}{}
	h.armLocked()
	h.mu.Unlock()
}

// NotifySettings 登记全局设置发生了变化。非阻塞。
func (h *Hub) NotifySettings() {
	h.mu.Lock()
	h.pendSettings = true
	h.armLocked()
	h.mu.Unlock()
}

// armLocked 在没有待触发的合并窗口时开一个。计时器在这里同步登记，
// 因此调用返回后假时钟的推进一定能触发它。
func (h *Hub) armLocked() {
	if h.armed {
		return
	}
	h.armed = true
	timer := h.clk.After(flushInterval)
	go func() {
		select {
		case <-timer:
			h.flush()
		case <-h.stop:
		}
	}()
}

// Start 启动对账循环，ctx 结束时关闭全部连接；返回的 channel 在一切收尾后关闭。
func (h *Hub) Start(ctx context.Context) <-chan struct{} {
	h.bmu.Lock()
	if list, err := h.cfg.Instances.List(ctx); err != nil {
		slog.Warn("读取实例列表失败，推送去重基线为空", "err", err)
	} else {
		h.last = make(map[string]model.Instance, len(list))
		for _, in := range list {
			h.last[in.ID] = in
		}
	}
	if h.cfg.Layouts != nil {
		if cur, err := h.cfg.Layouts.Current(ctx); err != nil {
			slog.Warn("读取当前布局失败，布局版本基线为空", "err", err)
		} else {
			h.layoutVer = cur.Version
		}
		h.screenRefreshLocked(ctx) // 建立推送去重基线，此时尚无连接
	}
	h.bmu.Unlock()

	timer := h.clk.After(reconcileInterval)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-timer:
				timer = h.clk.After(reconcileInterval)
				h.reconcile(ctx)
			case <-ctx.Done():
				h.Close()
				return
			}
		}
	}()
	return done
}

// Close 关闭全部连接并拒绝新连接；可重复调用，返回时所有连接已关闭。
func (h *Hub) Close() {
	h.bmu.Lock()
	h.closed = true
	list := make([]*client, 0, len(h.conns))
	for c := range h.conns {
		list = append(list, c)
	}
	h.bmu.Unlock()
	h.stopOnce.Do(func() { close(h.stop) })

	var wg sync.WaitGroup
	for _, c := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.kill(websocket.StatusGoingAway, "hub shutdown", false)
		}()
	}
	wg.Wait()
}

func (h *Hub) isClosed() bool {
	h.bmu.Lock()
	defer h.bmu.Unlock()
	return h.closed
}

func (h *Hub) connCount() int {
	h.bmu.Lock()
	defer h.bmu.Unlock()
	return len(h.conns)
}

// connsChanged 返回在下一次连接集合变化时被关闭的 channel。
func (h *Hub) connsChanged() <-chan struct{} {
	h.bmu.Lock()
	defer h.bmu.Unlock()
	return h.changed
}

// touchConnsLocked 通知连接集合发生了变化。
func (h *Hub) touchConnsLocked() {
	close(h.changed)
	h.changed = make(chan struct{})
}

// attach 构建 snapshot、登记连接并启动它的写线程。snapshot 是队列里的第一条消息。
// token 是握手时的会话令牌，供之后复核会话；为空表示不复核（测试用）。
func (h *Hub) attach(ctx context.Context, s sink, kind auth.SessionKind, token string) (*client, error) {
	c := newClient(h, ctx, s, kind, token)
	h.bmu.Lock()
	if h.closed {
		h.bmu.Unlock()
		c.cancel()
		return nil, errClosed
	}
	if err := h.resubscribeLocked(ctx, c, defaultTopics(kind)); err != nil {
		h.bmu.Unlock()
		c.cancel()
		return nil, err
	}
	h.conns[c] = struct{}{}
	if kind == auth.KindScreen {
		h.screenJoinedLocked()
	}
	h.touchConnsLocked()
	h.bmu.Unlock()
	go c.writeLoop()
	return c, nil
}

func (h *Hub) detach(c *client) {
	h.bmu.Lock()
	if _, ok := h.conns[c]; ok {
		delete(h.conns, c)
		if c.kind == auth.KindScreen {
			h.screenLeftLocked()
		}
		h.touchConnsLocked()
	}
	h.bmu.Unlock()
}

// subscribe 处理客户端的订阅请求：权限检查通过后换订阅集合并重发 snapshot。
func (h *Hub) subscribe(ctx context.Context, c *client, topics []string) {
	allowed := allowedTopics(c.kind)
	var denied, unknown []string
	for _, t := range topics {
		switch {
		case !knownTopic(t):
			unknown = append(unknown, t)
		case !slices.Contains(allowed, t):
			denied = append(denied, t)
		}
	}
	if len(unknown) > 0 {
		c.sendError(ui.ErrBadMessage, map[string]any{"topics": unknown})
		return
	}
	if len(denied) > 0 {
		c.sendError(ui.ErrSubscribeDenied, map[string]any{"topics": denied})
		return
	}
	h.bmu.Lock()
	err := h.resubscribeLocked(ctx, c, topics)
	h.bmu.Unlock()
	if err != nil {
		slog.Error("构建 snapshot 失败", "err", err)
		c.kill(websocket.StatusInternalError, "snapshot failed", false)
	}
}

// resubscribeLocked 按新订阅集合构建 snapshot、入队，并生效该订阅集合。须持有 bmu。
func (h *Hub) resubscribeLocked(ctx context.Context, c *client, topics []string) error {
	set := map[string]bool{}
	for _, t := range topics {
		set[t] = true
	}
	snap := ui.Snapshot{
		Type: ui.TypeSnapshot, Build: h.cfg.Build, ServerTime: h.clk.Now().UTC(),
		Role: roleOf(c.kind), Topics: sortedTopics(set), Instances: []model.Instance{},
	}
	if set[ui.TopicInstances] {
		list, err := h.cfg.Instances.List(ctx)
		if err != nil {
			return err
		}
		snap.Instances = list
	}
	if set[ui.TopicSettings] {
		if c.kind == auth.KindAdmin {
			s := h.cfg.Settings.Get()
			snap.Settings = &s
		} else {
			ss := screenSettings(h.cfg.Settings.Get())
			snap.ScreenSettings = &ss
		}
	}
	h.screenSnapshotLocked(ctx, c, set, &snap)
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	c.topics = set
	c.enqueue(raw)
	return nil
}

// outMsg 是一条待广播的 patch：admin 与 screen 两种会话各自的序列化结果（相同时共用）；
// 某一方为 nil 表示该角色不收这条消息。
type outMsg struct {
	topic         string
	admin, screen []byte
}

// flush 把合并窗口内登记的变化推给订阅者。
func (h *Hub) flush() {
	if h.cfg.afterFlush != nil {
		defer h.cfg.afterFlush()
	}
	h.mu.Lock()
	ids := make([]string, 0, len(h.pendInst))
	for id := range h.pendInst {
		ids = append(ids, id)
	}
	settingsChanged := h.pendSettings
	h.pendInst = map[string]struct{}{}
	h.pendSettings = false
	h.armed = false
	h.mu.Unlock()
	slices.Sort(ids)

	ctx := context.Background()
	h.bmu.Lock()
	defer h.bmu.Unlock()
	if h.closed {
		return
	}
	var msgs []outMsg
	for _, id := range ids {
		in, err := h.cfg.Instances.View(ctx, id)
		switch {
		case errors.Is(err, instances.ErrNotFound):
			msgs = appendMsg(msgs, h.removedMsgLocked(id))
		case err != nil:
			// 读取失败不丢变化：交给下一轮对账。
			slog.Warn("读取实例失败，等待下一轮对账", "instance", id, "err", err)
		default:
			msgs = appendMsg(msgs, h.instanceMsgLocked(in))
		}
	}
	if settingsChanged {
		msgs = appendMsg(msgs, h.settingsMsgLocked())
	}
	// 实例变化可能影响屏幕引用的数据与解析后布局的展示状态；设置变化可能改变语言。
	if len(ids) > 0 || settingsChanged {
		msgs = append(msgs, h.screenRefreshLocked(ctx)...)
	}
	h.deliverLocked(ctx, msgs)
}

// reconcile 对账：与最近一次广播的状态比较，把没有变更通知的变化（如展示状态过期）推出去。
func (h *Hub) reconcile(ctx context.Context) {
	h.bmu.Lock()
	defer h.bmu.Unlock()
	if h.closed {
		return
	}
	list, err := h.cfg.Instances.List(ctx)
	if err != nil {
		slog.Warn("对账时读取实例列表失败", "err", err)
		return
	}
	var msgs []outMsg
	seen := make(map[string]bool, len(list))
	for _, in := range list {
		seen[in.ID] = true
		msgs = appendMsg(msgs, h.instanceMsgLocked(in))
	}
	for id := range h.last {
		if !seen[id] {
			msgs = appendMsg(msgs, h.removedMsgLocked(id))
		}
	}
	msgs = append(msgs, h.screenRefreshLocked(ctx)...)
	h.deliverLocked(ctx, msgs)
}

func (m outMsg) payload(kind auth.SessionKind) []byte {
	if kind == auth.KindAdmin {
		return m.admin
	}
	return m.screen
}

func appendMsg(msgs []outMsg, m *outMsg) []outMsg {
	if m == nil {
		return msgs
	}
	return append(msgs, *m)
}

// instanceMsgLocked 记下并序列化实例状态；与最近一次广播的状态相同时返回 nil。
func (h *Hub) instanceMsgLocked(in model.Instance) *outMsg {
	if h.last == nil {
		h.last = map[string]model.Instance{}
	}
	if prev, ok := h.last[in.ID]; ok && reflect.DeepEqual(prev, in) {
		return nil
	}
	h.last[in.ID] = in
	return h.patchMsg(ui.TopicInstances, ui.Patch{
		Type: ui.TypePatch, ServerTime: h.clk.Now().UTC(), Entity: ui.EntityInstanceState, Instance: &in,
	}, nil)
}

func (h *Hub) removedMsgLocked(id string) *outMsg {
	delete(h.last, id)
	return h.patchMsg(ui.TopicInstances, ui.Patch{
		Type: ui.TypePatch, ServerTime: h.clk.Now().UTC(), Entity: ui.EntityInstanceRemoved, ID: id,
	}, nil)
}

func (h *Hub) settingsMsgLocked() *outMsg {
	cur := h.cfg.Settings.Get()
	now := h.clk.Now().UTC()
	admin := ui.Patch{Type: ui.TypePatch, ServerTime: now, Entity: ui.EntitySettings, Settings: &cur}
	ss := screenSettings(cur)
	screen := ui.Patch{Type: ui.TypePatch, ServerTime: now, Entity: ui.EntitySettings, ScreenSettings: &ss}
	return h.patchMsg(ui.TopicSettings, admin, &screen)
}

// patchMsg 序列化 patch；screen 为 nil 表示屏幕会话用同一份（实例 patch 屏幕本就收不到）。
func (h *Hub) patchMsg(topic string, admin ui.Patch, screen *ui.Patch) *outMsg {
	adminRaw, err := json.Marshal(admin)
	if err != nil {
		slog.Error("序列化 patch 失败", "err", err)
		return nil
	}
	m := &outMsg{topic: topic, admin: adminRaw, screen: adminRaw}
	if screen != nil {
		if m.screen, err = json.Marshal(screen); err != nil {
			slog.Error("序列化 patch 失败", "err", err)
			return nil
		}
	}
	return m
}

// deliverLocked 把一轮广播的消息入队给订阅者。某个连接在这一轮要收的条数超过队列容量的一半时，
// 改为给它发一份 snapshot：既不会把健康连接的队列冲满而误判为慢连接，批量变化时数据量也更小。
func (h *Hub) deliverLocked(ctx context.Context, msgs []outMsg) {
	if len(msgs) == 0 {
		return
	}
	limit := max(h.cfg.QueueSize/2, 1)
	for c := range h.conns {
		var rel []outMsg
		for _, m := range msgs {
			if c.topics[m.topic] && m.payload(c.kind) != nil {
				rel = append(rel, m)
			}
		}
		if len(rel) > limit {
			if err := h.resubscribeLocked(ctx, c, sortedTopics(c.topics)); err == nil {
				continue
			} else {
				slog.Warn("批量变化时构建 snapshot 失败，改发逐条 patch", "err", err)
			}
		}
		for _, m := range rel {
			c.enqueue(m.payload(c.kind))
		}
	}
}

// RecheckSessions 让所有连接立即复核会话；会话被撤销（登出、吊销）时由 auth.Sessions 的回调触发。
// 非阻塞：复核在后台 goroutine 里做，不持有 bmu 逐个查库。
func (h *Hub) RecheckSessions() {
	h.bmu.Lock()
	list := make([]*client, 0, len(h.conns))
	for c := range h.conns {
		list = append(list, c)
	}
	h.bmu.Unlock()
	go func() {
		for _, c := range list {
			h.recheck(c)
		}
	}()
}

// recheck 重新查会话：查不到、类型变了即以 policy violation 断开。查询出错时保持连接，下次再查。
func (h *Hub) recheck(c *client) {
	if c.token == "" {
		return
	}
	kind, ok, err := h.cfg.Sessions.Lookup(c.ctx, c.token)
	if err != nil {
		slog.Warn("复核 WebSocket 会话失败", "err", err)
		return
	}
	if !ok || kind != c.kind {
		c.kill(websocket.StatusPolicyViolation, "session revoked", false)
	}
}

// defaultTopics 是会话建立时的默认订阅：管理员看实例、设置、原始布局与屏幕状态；
// 屏幕会话只看设置、解析后布局、屏幕状态与布局引用实例的数据。
func defaultTopics(kind auth.SessionKind) []string {
	if kind == auth.KindAdmin {
		return []string{ui.TopicInstances, ui.TopicSettings, ui.TopicLayout, ui.TopicScreenState}
	}
	return []string{ui.TopicSettings, ui.TopicLayout, ui.TopicScreenState, ui.TopicScreenData}
}

// allowedTopics 是会话可订阅的主题：管理员在默认之外可订阅 screen_data 做预览；屏幕会话不能订阅 instances。
func allowedTopics(kind auth.SessionKind) []string {
	if kind == auth.KindAdmin {
		return append(defaultTopics(kind), ui.TopicScreenData)
	}
	return defaultTopics(kind)
}

func roleOf(kind auth.SessionKind) string {
	if kind == auth.KindAdmin {
		return ui.RoleAdmin
	}
	return ui.RoleScreen
}

func sortedTopics(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

func screenSettings(s model.Settings) ui.ScreenSettings {
	return ui.ScreenSettings{Language: s.Language, Timezone: s.Timezone, ReduceEffects: s.ReduceEffects, Screen: s.Screen}
}
