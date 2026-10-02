package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"

	"github.com/LanceLRQ/PiMon/src/internal/hub/auth"
	"github.com/LanceLRQ/PiMon/src/internal/hub/instances"
	"github.com/LanceLRQ/PiMon/src/internal/hub/screens"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/ui"
)

// maxCurrentScreenLen 是屏幕上报的 screen id 的长度上限，超出的上报被忽略。
const maxCurrentScreenLen = 64

// LayoutSource 是屏幕布局的来源，由 screens.Service 实现。
type LayoutSource interface {
	// Current 返回当前布局（原始，带版本），管理员会话使用。
	Current(ctx context.Context) (model.LayoutState, error)
	// Resolve 返回解析后的当前布局，屏幕会话使用。
	Resolve(ctx context.Context, lang string) (model.ResolvedLayout, error)
}

// ScreenStateSource 是屏幕当前状态的来源，由 screenstate.Service 实现。
type ScreenStateSource interface {
	State() model.ScreenState
}

// ScreenDataSource 是屏幕渲染所需的实例数据来源，由 instances.Service 实现。
// 实例不存在时返回 instances.ErrNotFound。
type ScreenDataSource interface {
	ScreenData(ctx context.Context, id string) (model.ScreenInstanceData, error)
}

// ScreenSink 接收屏幕会话上报的信息与在线状态，由 screenstate.Service 实现。
// ReportViewport、ReportCoarsePointer、ReportCurrentScreen 在会话读循环里调用；
// 只有 SetScreenOnline 会在持有广播锁时被调用。所有方法都必须非阻塞，且不得回调 Hub。
type ScreenSink interface {
	ReportViewport(v model.Viewport)
	ReportCoarsePointer(coarse bool)
	ReportCurrentScreen(id string)
	// SetScreenOnline 在屏幕会话从无到有（在线）与从有到无（离线）时调用。
	SetScreenOnline(online bool)
}

func knownTopic(t string) bool {
	switch t {
	case ui.TopicInstances, ui.TopicSettings, ui.TopicLayout, ui.TopicScreenState, ui.TopicScreenData:
		return true
	}
	return false
}

// NotifyLayout 把保存或回滚后的新布局立即推给订阅者（Ruling 21，不进合并窗口）：
// 管理员收原始布局与版本，屏幕会话收解析后的布局与新引用实例的数据。
// screens.Service 的回调在锁外调用，并发保存时可能乱序，这里按版本丢弃较旧的回调。
func (h *Hub) NotifyLayout(st model.LayoutState) {
	h.bmu.Lock()
	defer h.bmu.Unlock()
	if h.closed || st.Version <= h.layoutVer {
		return
	}
	h.layoutVer = st.Version
	var msgs []outMsg
	adminOnly := h.patchMsg(ui.TopicLayout, ui.Patch{
		Type: ui.TypePatch, ServerTime: h.clk.Now().UTC(), Entity: ui.EntityLayout, Layout: &st,
	}, nil)
	if adminOnly != nil {
		adminOnly.screen = nil // 屏幕会话收的是解析后的布局，由 screenRefreshLocked 产生
		msgs = append(msgs, *adminOnly)
	}
	msgs = append(msgs, h.screenRefreshLocked(context.Background())...)
	h.deliverLocked(context.Background(), msgs)
}

// NotifyScreenState 把屏幕状态变化立即推给订阅者（Ruling 21）。
func (h *Hub) NotifyScreenState(st model.ScreenState) {
	h.bmu.Lock()
	defer h.bmu.Unlock()
	if h.closed {
		return
	}
	m := h.patchMsg(ui.TopicScreenState, ui.Patch{
		Type: ui.TypePatch, ServerTime: h.clk.Now().UTC(), Entity: ui.EntityScreenState, ScreenState: &st,
	}, nil)
	if m != nil {
		h.deliverLocked(context.Background(), []outMsg{*m})
	}
}

// SendScreenControl 把一次性指令立即发给全部屏幕会话（Ruling 21），返回收到指令的会话数；
// 为 0 说明此刻没有屏幕在线，指令未送达。
func (h *Hub) SendScreenControl(opID int64, action, screenID string) int {
	raw, err := json.Marshal(ui.ScreenControl{
		Type: ui.TypeScreenControl, ServerTime: h.clk.Now().UTC(), Action: action, ScreenID: screenID, OpID: opID,
	})
	if err != nil {
		slog.Error("序列化屏幕指令失败", "err", err)
		return 0
	}
	h.bmu.Lock()
	defer h.bmu.Unlock()
	n := 0
	for c := range h.conns {
		if c.kind == auth.KindScreen {
			c.enqueue(raw)
			n++
		}
	}
	return n
}

// handleReport 处理 viewport_report：只有屏幕会话的上报有效，交给 ScreenSink。
func (h *Hub) handleReport(c *client, msg ui.ClientMessage) {
	if c.kind != auth.KindScreen || h.cfg.Sink == nil {
		return
	}
	if msg.Viewport != nil {
		h.cfg.Sink.ReportViewport(*msg.Viewport)
	}
	if msg.CoarsePointer != nil {
		h.cfg.Sink.ReportCoarsePointer(*msg.CoarsePointer)
	}
	if id := msg.CurrentScreen; id != "" && len(id) <= maxCurrentScreenLen {
		h.cfg.Sink.ReportCurrentScreen(id)
	}
}

// screenJoinedLocked 与 screenLeftLocked 在屏幕会话登记与注销时维护在线计数，并在
// 在线状态切换时通知 ScreenSink。须持有 bmu。
func (h *Hub) screenJoinedLocked() {
	h.screenConns++
	if h.screenConns == 1 && h.cfg.Sink != nil {
		h.cfg.Sink.SetScreenOnline(true)
	}
}

func (h *Hub) screenLeftLocked() {
	h.screenConns--
	if h.screenConns == 0 && h.cfg.Sink != nil {
		h.cfg.Sink.SetScreenOnline(false)
	}
}

// screenSnapshotLocked 按订阅集合与角色填充 snapshot 里的屏幕相关字段。须持有 bmu。
// 布局读取或解析失败只记 warn 并把对应字段留空（与 screenRefreshLocked 的降级一致），
// 不让整条连接建立失败；之后的对账会补推。
func (h *Hub) screenSnapshotLocked(ctx context.Context, c *client, set map[string]bool, snap *ui.Snapshot) {
	if set[ui.TopicScreenState] && h.cfg.ScreenState != nil {
		st := h.cfg.ScreenState.State()
		snap.ScreenState = &st
	}
	admin := c.kind == auth.KindAdmin
	if h.cfg.Layouts == nil {
		return
	}
	if set[ui.TopicLayout] && admin {
		if cur, err := h.cfg.Layouts.Current(ctx); err != nil {
			slog.Warn("读取当前布局失败，snapshot 的 layout 留空", "err", err)
		} else {
			snap.Layout = &cur
		}
	}
	if (set[ui.TopicLayout] && !admin) || set[ui.TopicScreenData] {
		resolved, err := h.cfg.Layouts.Resolve(ctx, h.cfg.Settings.Get().Language)
		if err != nil {
			slog.Warn("解析屏幕布局失败，snapshot 的布局与数据留空", "err", err)
			return
		}
		if set[ui.TopicLayout] && !admin {
			snap.ResolvedLayout = &resolved
		}
		if set[ui.TopicScreenData] && h.cfg.ScreenData != nil {
			data := h.collectDataLocked(ctx, screens.ReferencedInstanceIDs(resolved), nil)
			snap.ScreenData = &data
		}
	}
}

// collectDataLocked 读取实例数据；prev 非 nil 时只返回与上次广播不同的，并更新 prev。读取失败的实例被跳过。
func (h *Hub) collectDataLocked(ctx context.Context, ids []string, prev map[string]model.ScreenInstanceData) []model.ScreenInstanceData {
	out := []model.ScreenInstanceData{}
	for _, id := range ids {
		d, err := h.cfg.ScreenData.ScreenData(ctx, id)
		if err != nil {
			if !errors.Is(err, instances.ErrNotFound) {
				slog.Warn("读取屏幕实例数据失败", "instance", id, "err", err)
			}
			continue
		}
		if prev != nil {
			if old, ok := prev[id]; ok && reflect.DeepEqual(old, d) {
				continue
			}
			prev[id] = d
		}
		out = append(out, d)
	}
	return out
}

// screenRefreshLocked 重新解析当前布局并读取引用实例的数据，与最近一次广播的比较，
// 产出屏幕会话要收的 layout（解析后）与 screen_data patch。布局版本、实例增删、展示状态变化、
// 语言变化都会让解析结果变化。须持有 bmu。
func (h *Hub) screenRefreshLocked(ctx context.Context) []outMsg {
	if h.cfg.Layouts == nil {
		return nil
	}
	resolved, err := h.cfg.Layouts.Resolve(ctx, h.cfg.Settings.Get().Language)
	if err != nil {
		slog.Warn("解析屏幕布局失败，等待下一轮对账", "err", err)
		return nil
	}
	var msgs []outMsg
	now := h.clk.Now().UTC()
	raw, err := json.Marshal(resolved)
	if err == nil && !bytes.Equal(raw, h.lastResolved) {
		h.lastResolved = raw
		if m := h.patchMsg(ui.TopicLayout, ui.Patch{
			Type: ui.TypePatch, ServerTime: now, Entity: ui.EntityLayout, ResolvedLayout: &resolved,
		}, nil); m != nil {
			m.admin = nil // 管理员收的是原始布局
			msgs = append(msgs, *m)
		}
	}
	if h.cfg.ScreenData == nil {
		return msgs
	}
	ids := screens.ReferencedInstanceIDs(resolved)
	if h.lastData == nil {
		h.lastData = map[string]model.ScreenInstanceData{}
	}
	for id := range h.lastData {
		if !slices.Contains(ids, id) {
			delete(h.lastData, id)
		}
	}
	if changed := h.collectDataLocked(ctx, ids, h.lastData); len(changed) > 0 {
		if m := h.patchMsg(ui.TopicScreenData, ui.Patch{
			Type: ui.TypePatch, ServerTime: now, Entity: ui.EntityScreenData, ScreenData: changed,
		}, nil); m != nil {
			msgs = append(msgs, *m)
		}
	}
	return msgs
}
