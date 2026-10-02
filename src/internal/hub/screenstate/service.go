package screenstate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

const (
	defaultWakeMinutes = 30
	maxWakeMinutes     = 24 * 60
)

// Config 是屏幕状态服务的依赖。
type Config struct {
	DB    *store.DB
	Clock clock.Clock
	// Timezone 返回当前全局时区名；每次计算时现取，设置修改后调用 Refresh 立即生效。
	Timezone func() string
}

// Command 是一次性屏幕指令（refresh、switch），由调用方转发给屏幕会话。
type Command struct {
	// OpID 是对应的操作记录 id；送达屏幕后调用 MarkDelivered。
	OpID     int64
	Action   string
	ScreenID string
}

// Service 维护时段计划、远程临时操作与屏幕状态。远程临时操作只存内存，hub 重启后回到时段计划。
type Service struct {
	db  *store.DB
	clk clock.Clock
	tz  func() string

	mu       sync.Mutex
	sched    model.Schedule
	ov       *override
	last     model.ScreenState
	haveLast bool
	coarse   *bool
	current  string
	online   bool
	lastSeen *time.Time
	onChange func(model.ScreenState)
	onCmd    func(Command)
	onVP     func(model.Viewport)

	// emitMu 保证状态变化的通知按计算顺序发出。
	emitMu sync.Mutex
	kick   chan struct{}
	vp     *ViewportTracker
}

// New 创建服务；使用前需要 Load。
func New(c Config) *Service {
	tz := c.Timezone
	if tz == nil {
		tz = func() string { return "UTC" }
	}
	s := &Service{db: c.DB, clk: c.Clock, tz: tz, sched: DefaultSchedule(), kick: make(chan struct{}, 1)}
	s.vp = NewViewportTracker(c.Clock, s.fireViewport)
	return s
}

// OnChange 注册屏幕状态变化的回调：状态（含 until、next_change）与上次通知的不同才调用，
// 同步执行且不持 mu，必须非阻塞；回调不可重入：执行期间持有通知顺序锁，
// 不得在回调里调用 Refresh、Control、SetSchedule 等会再次计算并通知的方法（会死锁）。
// 重复注册会覆盖前一个。
func (s *Service) OnChange(f func(model.ScreenState)) {
	s.mu.Lock()
	s.onChange = f
	s.mu.Unlock()
}

// OnCommand 注册一次性指令（refresh、switch）的出口，调用约定同 OnChange。
func (s *Service) OnCommand(f func(Command)) {
	s.mu.Lock()
	s.onCmd = f
	s.mu.Unlock()
}

// OnViewport 注册 viewport 被采信后的回调，调用约定同 OnChange。
func (s *Service) OnViewport(f func(model.Viewport)) {
	s.mu.Lock()
	s.onVP = f
	s.mu.Unlock()
}

func (s *Service) fireViewport(v model.Viewport) {
	s.mu.Lock()
	f := s.onVP
	s.mu.Unlock()
	if f != nil {
		f(v)
	}
}

// Load 读取库中的时段计划（没有或不合法时用默认计划），并计算初始状态。
func (s *Service) Load(ctx context.Context) error {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT schedule_json FROM schedule WHERE id = 1`).Scan(&raw)
	sched := DefaultSchedule()
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("读取时段计划: %w", err)
	default:
		var got model.Schedule
		if jerr := json.Unmarshal([]byte(raw), &got); jerr != nil || len(ValidateSchedule(got)) > 0 {
			slog.Warn("库中的时段计划不合法，已改用默认计划", "err", jerr)
		} else {
			sched = normalizeSchedule(got)
		}
	}
	s.mu.Lock()
	s.sched = sched
	st := s.computeLocked()
	s.last, s.haveLast = st, true
	s.vp.SetScreenOn(st.Mode == model.ScreenModeOn)
	s.mu.Unlock()
	return nil
}

// Schedule 返回当前时段计划的副本。
func (s *Service) Schedule() model.Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return model.Schedule{Periods: append([]model.SchedulePeriod(nil), s.sched.Periods...)}
}

// SetSchedule 校验并保存时段计划（整份覆盖，后写覆盖），随即重算状态；不合法返回 *InvalidError。
func (s *Service) SetSchedule(ctx context.Context, n model.Schedule) error {
	if problems := ValidateSchedule(n); len(problems) > 0 {
		return &InvalidError{Problems: problems}
	}
	n = normalizeSchedule(n)
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO schedule (id, schedule_json, updated_at) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET schedule_json = excluded.schedule_json, updated_at = excluded.updated_at`,
		string(raw), store.FormatTime(s.clk.Now())); err != nil {
		return fmt.Errorf("保存时段计划: %w", err)
	}
	s.mu.Lock()
	s.sched = n
	s.reanchorLocked()
	s.mu.Unlock()
	s.recompute()
	s.wake()
	return nil
}

// reanchorLocked 在时段计划或时区变化后，把远程开屏、关屏的到期点按新计划重算为下一个时段边界
// （临时亮屏是固定时长，不受影响）。调用方持 mu。
func (s *Service) reanchorLocked() {
	now := s.clk.Now()
	if !s.ov.activeAt(now) || s.ov.kind == overlayWake {
		return
	}
	s.ov = newOverride(s.sched, s.location(), now, s.ov.kind, 0)
}

// location 返回全局时区；名称非法时回退 UTC。
func (s *Service) location() *time.Location {
	loc, err := time.LoadLocation(s.tz())
	if err != nil {
		return time.UTC
	}
	return loc
}

// computeLocked 按当前时刻计算状态，并清掉已过期的临时操作。调用方持 mu。
func (s *Service) computeLocked() model.ScreenState {
	now := s.clk.Now()
	if s.ov != nil && !s.ov.activeAt(now) {
		s.ov = nil
	}
	return compute(s.sched, s.location(), now, s.ov)
}

// State 返回此刻的屏幕状态。
func (s *Service) State() model.ScreenState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.computeLocked()
}

// Status 返回状态、已采信的 viewport 与触摸能力。
func (s *Service) Status() model.ScreenStatus {
	s.mu.Lock()
	st := model.ScreenStatus{State: s.computeLocked()}
	if s.coarse != nil {
		c := *s.coarse
		st.CoarsePointer = &c
	}
	st.CurrentScreen = s.current
	st.Online = s.online
	if s.lastSeen != nil {
		t := *s.lastSeen
		st.LastSeen = &t
	}
	s.mu.Unlock()
	st.Viewport = s.vp.Current()
	return st
}

// ReportCurrentScreen 记录屏幕会话正在显示的 screen id。
func (s *Service) ReportCurrentScreen(id string) {
	s.mu.Lock()
	s.current = id
	s.mu.Unlock()
}

// SetScreenOnline 记录屏幕会话的在线状态：有屏幕会话的 WebSocket 连着即在线；
// 每次在线与离线的切换都更新最近在线时间。
func (s *Service) SetScreenOnline(online bool) {
	now := s.clk.Now().UTC()
	s.mu.Lock()
	s.online = online
	s.lastSeen = &now
	s.mu.Unlock()
}

// Online 返回屏幕是否在线，以及最近一次在线或掉线的时刻（从未连过为 nil）。
func (s *Service) Online() (online bool, lastSeen *time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSeen != nil {
		t := *s.lastSeen
		lastSeen = &t
	}
	return s.online, lastSeen
}

// ReportViewport 处理屏幕会话上报的 viewport（来源校验由调用方负责）。
func (s *Service) ReportViewport(v model.Viewport) { s.vp.Report(v) }

// ReportCoarsePointer 记录屏幕上报的触摸（粗指针）能力。
func (s *Service) ReportCoarsePointer(coarse bool) {
	s.mu.Lock()
	s.coarse = &coarse
	s.mu.Unlock()
}

// Refresh 立即重算状态；时区等外部输入变化后调用，远程开屏、关屏的到期点随之按新时区重算。
func (s *Service) Refresh() {
	s.mu.Lock()
	s.reanchorLocked()
	s.mu.Unlock()
	s.recompute()
	s.wake()
}

func (s *Service) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// recompute 重算状态，与上次通知的不同就通知订阅者，并同步 viewport 采信器的开关屏状态。
func (s *Service) recompute() model.ScreenState {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	s.mu.Lock()
	st := s.computeLocked()
	changed := !s.haveLast || !sameState(st, s.last)
	s.last, s.haveLast = st, true
	s.vp.SetScreenOn(st.Mode == model.ScreenModeOn)
	cb := s.onChange
	s.mu.Unlock()
	if changed && cb != nil {
		cb(st)
	}
	return st
}

func sameState(a, b model.ScreenState) bool {
	return a.Mode == b.Mode && a.ThemeID == b.ThemeID && a.Reason == b.Reason &&
		sameTime(a.Until, b.Until) && sameTime(a.NextChange, b.NextChange)
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// Run 在变化点重算状态直到 ctx 结束：每次睡到下一个时段边界或临时操作到期点，
// 且最多睡 60 秒，防冷启动时系统时钟被校时跳变。
func (s *Service) Run(ctx context.Context) {
	for {
		s.recompute()
		s.mu.Lock()
		d := sleepFor(s.sched, s.location(), s.clk.Now(), s.ov)
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-s.clk.After(d):
		case <-s.kick:
		}
	}
}

// Control 校验并执行一次远程控制：写操作记录，开关屏与临时亮屏改变状态并通知，
// refresh、switch 经 OnCommand 转发。校验失败返回 model.FieldErrors，不写记录。
func (s *Service) Control(ctx context.Context, req model.ScreenControlRequest, clientIP string) (model.ScreenControlResponse, error) {
	params := map[string]any{}
	var kind overlay
	minutes := 0
	stateful := true
	switch req.Action {
	case model.ScreenActionRefresh:
		stateful = false
	case model.ScreenActionSwitch:
		stateful = false
		id := strings.TrimSpace(req.ScreenID)
		if id == "" {
			return model.ScreenControlResponse{}, model.FieldErrors{"screen_id": model.FieldRequired}
		}
		req.ScreenID = id
		params["screen_id"] = id
	case model.ScreenActionOn:
		kind = overlayOn
	case model.ScreenActionOff:
		kind = overlayOff
	case model.ScreenActionWake:
		kind = overlayWake
		minutes = req.Minutes
		if minutes == 0 {
			minutes = defaultWakeMinutes
		}
		if minutes < 0 || minutes > maxWakeMinutes {
			return model.ScreenControlResponse{}, model.FieldErrors{"minutes": model.FieldOutOfRange}
		}
		params["minutes"] = minutes
	default:
		return model.ScreenControlResponse{}, model.FieldErrors{"action": model.FieldInvalid}
	}

	op, err := s.RecordOp(ctx, req.Action, params, clientIP)
	if err != nil {
		return model.ScreenControlResponse{}, err
	}
	if stateful {
		s.mu.Lock()
		s.ov = newOverride(s.sched, s.location(), s.clk.Now(), kind, minutes)
		s.mu.Unlock()
		st := s.recompute()
		s.wake()
		return model.ScreenControlResponse{Op: op, State: st}, nil
	}
	s.mu.Lock()
	cb := s.onCmd
	st := s.computeLocked()
	s.mu.Unlock()
	if cb != nil {
		cb(Command{OpID: op.ID, Action: req.Action, ScreenID: req.ScreenID})
	}
	return model.ScreenControlResponse{Op: op, State: st}, nil
}
