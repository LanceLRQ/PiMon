package history

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// 默认值与聚合桶宽。
const (
	// DefaultFlushInterval 是内存缓冲写入原始表的间隔（设计 3.4）。
	DefaultFlushInterval = 60 * time.Second
	// DefaultMaxBuffered 是内存缓冲的样本数上限。数据库写不进去时缓冲会保留重试，
	// 超过上限就丢弃最旧的并计数，避免 SD 卡故障时内存无限增长；
	// 每个样本约 64 字节，20 万条约 13 MB，足够 30 实例 × 15 项 × 10 秒间隔缓冲约 1 小时。
	DefaultMaxBuffered = 200000

	step5m = int64(5 * time.Minute / time.Millisecond)
	step1h = int64(time.Hour / time.Millisecond)

	metaAgg5m = "history.agg5m"
	metaAgg1h = "history.agg1h"
)

// ErrInstanceNotFound 表示查询的实例不存在。
var ErrInstanceNotFound = errors.New("实例不存在")

// Config 是历史服务的依赖。
type Config struct {
	DB    *store.DB
	Clock clock.Clock
	// Retention 每次清理与查询时现取，设置可在运行时修改。
	Retention func() model.RetentionSettings
	// Logger 缺省用 slog.Default()。
	Logger *slog.Logger
	// FlushInterval 缺省 DefaultFlushInterval；MaxBuffered 缺省 DefaultMaxBuffered。
	FlushInterval time.Duration
	MaxBuffered   int
}

// Query 是历史查询参数。Field 为空取数据项的默认字段。
type Query struct {
	InstanceID string
	Item       string
	Field      string
	Range      string
}

type sample struct {
	id, item, field string
	ts              int64
	v               float64
}

type seriesKey struct{ id, item, field string }

// Service 是数值历史服务。所有方法可并发使用。
type Service struct {
	db        *store.DB
	clk       clock.Clock
	retention func() model.RetentionSettings
	log       *slog.Logger
	every     time.Duration
	maxBuf    int

	writeErrs atomic.Int64
	dropped   atomic.Int64

	// flushMu 串行化写盘；mu 保护缓冲与序列表，持有 mu 时不访问数据库。
	flushMu sync.Mutex
	mu      sync.Mutex
	buf     []sample
	// known 记录原始表里有数据的序列及其最近样本时间，聚合与原始表清理都按序列做主键范围读写，
	// 不必全表扫描；首次使用前从数据库加载一次，原始表清理后剔除已无数据的序列。
	known       map[seriesKey]int64
	knownLoaded bool
}

// New 创建历史服务。
func New(c Config) *Service {
	s := &Service{
		db: c.DB, clk: c.Clock, retention: c.Retention, log: c.Logger,
		every: c.FlushInterval, maxBuf: c.MaxBuffered, known: map[seriesKey]int64{},
	}
	if s.clk == nil {
		s.clk = clock.Real{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.every <= 0 {
		s.every = DefaultFlushInterval
	}
	if s.maxBuf <= 0 {
		s.maxBuf = DefaultMaxBuffered
	}
	return s
}

// recordedFields 是各数据项类型要记入历史的字段：首个是 report 包定义的默认字段，
// 其后是额外记录的字段（quota 另记 used）。字段集与默认字段以 report 包为唯一来源。
var recordedFields = buildRecordedFields()

func buildRecordedFields() map[string][]string {
	extra := map[string][]string{report.TypeQuota: {"used"}}
	m := map[string][]string{}
	for _, t := range []string{report.TypeGauge, report.TypeNumber, report.TypeQuota, report.TypeMoney} {
		def, ok := report.DefaultField(t)
		if !ok {
			panic("数据项类型 " + t + " 没有默认字段")
		}
		m[t] = append([]string{def}, extra[t]...)
	}
	return m
}

// itemValue 取数据项的数值字段；缺失返回 nil。
func itemValue(it *report.Item, field string) *float64 {
	switch field {
	case "value":
		return it.Value
	case "remaining_pct":
		return it.RemainingPct
	case "amount":
		return it.Amount
	case "used":
		return it.Used
	}
	return nil
}

func isRecordedField(field string) bool {
	for _, fs := range recordedFields {
		for _, f := range fs {
			if f == field {
				return true
			}
		}
	}
	return false
}

// Record 实现 instances.HistoryRecorder：只做内存攒批，快速返回。
// 缺失的数值（nil）、非有限值、被标记为过期的旧值都不记录，也不会被当作 0。
func (s *Service) Record(instanceID string, at time.Time, rep *report.Report) {
	if rep == nil || rep.Stale {
		return
	}
	ts := at.UnixMilli()
	var batch []sample
	for i := range rep.Items {
		it := &rep.Items[i]
		if it.Stale {
			continue
		}
		for _, f := range recordedFields[it.Type] {
			v := itemValue(it, f)
			if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
				continue
			}
			batch = append(batch, sample{id: instanceID, item: it.Key, field: f, ts: ts, v: *v})
		}
	}
	if len(batch) == 0 {
		return
	}
	s.mu.Lock()
	s.buf = append(s.buf, batch...)
	s.trimLocked()
	s.mu.Unlock()
}

// trimLocked 在缓冲超过上限时丢弃最旧的样本并计数。
func (s *Service) trimLocked() {
	if over := len(s.buf) - s.maxBuf; over > 0 {
		s.buf = s.buf[over:]
		s.dropped.Add(int64(over))
	}
}

// WriteErrors 返回历史写盘（写入、聚合、清理）失败的累计次数，供 hub-self 与
// 实例当前状态落盘失败数相加。
func (s *Service) WriteErrors() int64 { return s.writeErrs.Load() }

// Dropped 返回因缓冲超限而丢弃的样本数。
func (s *Service) Dropped() int64 { return s.dropped.Load() }

func (s *Service) fail(err error) error {
	if err != nil {
		s.writeErrs.Add(1)
	}
	return err
}

// Flush 把内存缓冲写入原始表。失败时整批放回缓冲等待下次重试（受上限约束）。
// 已被删除的实例的样本会被跳过，不会让整批失败。
func (s *Service) Flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	batch := s.buf
	s.buf = nil
	s.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	if err := s.writeRaw(ctx, batch); err != nil {
		s.mu.Lock()
		s.buf = append(batch, s.buf...)
		s.trimLocked()
		s.mu.Unlock()
		return s.fail(err)
	}
	s.mu.Lock()
	for _, x := range batch {
		k := seriesKey{x.id, x.item, x.field}
		if x.ts > s.known[k] {
			s.known[k] = x.ts
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) writeRaw(ctx context.Context, batch []sample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := tx.PrepareContext(ctx, `
INSERT INTO history_raw (instance_id, item, field, ts, v)
SELECT ?1, ?2, ?3, ?4, ?5 WHERE EXISTS (SELECT 1 FROM plugin_instances WHERE id = ?1)
ON CONFLICT (instance_id, item, field, ts) DO UPDATE SET v = excluded.v`)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	for _, x := range batch {
		if _, err := st.ExecContext(ctx, x.id, x.item, x.field, x.ts, x.v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) metaInt(ctx context.Context, key string) (int64, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, nil // 内容损坏按从未聚合处理，重算是幂等的
	}
	return n, nil
}

// seriesSnapshot 返回原始表里有数据的序列；首次调用时从原始表加载一次。
func (s *Service) seriesSnapshot(ctx context.Context) ([]seriesKey, error) {
	s.mu.Lock()
	loaded := s.knownLoaded
	s.mu.Unlock()
	if !loaded {
		rows, err := s.db.QueryContext(ctx, `SELECT instance_id, item, field, MAX(ts) FROM history_raw GROUP BY instance_id, item, field`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		found := map[seriesKey]int64{}
		for rows.Next() {
			var k seriesKey
			var ts int64
			if err := rows.Scan(&k.id, &k.item, &k.field, &ts); err != nil {
				return nil, err
			}
			found[k] = ts
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		for k, ts := range found {
			if ts > s.known[k] {
				s.known[k] = ts
			}
		}
		s.knownLoaded = true
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]seriesKey, 0, len(s.known))
	for k := range s.known {
		out = append(out, k)
	}
	return out, nil
}

// pruneKnown 剔除最近样本早于 floor 的序列：它们在原始表里已无数据。
func (s *Service) pruneKnown(floor int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, last := range s.known {
		if last < floor {
			delete(s.known, k)
		}
	}
}

// ceilTo 把 v 向上对齐到 step 的整数倍。
func ceilTo(v, step int64) int64 { return (v + step - 1) / step * step }

// Aggregate5m 把已结束的 5 分钟桶从原始表聚合到 5 分钟表（avg/min/max/样本数）。
// 用 meta 里的水位线记录已聚合到哪里，下次从上一个桶起重算（覆盖写入），
// 所以重复执行、重启后重跑都是幂等的；只从仍在原始保留期内的完整桶起算，
// 避免原始数据被部分清理后把已聚合的桶用残缺数据覆盖。
func (s *Service) Aggregate5m(ctx context.Context) error {
	now := s.clk.Now().UnixMilli()
	rawFloor := now - int64(s.retention().RawHours)*step1h
	return s.fail(s.aggregate(ctx, aggSpec{
		meta: metaAgg5m, dst: "history_5m", step: step5m, now: now,
		lo: ceilTo(rawFloor, step5m),
		sel: `SELECT instance_id, item, field, (ts / ?1) * ?1, AVG(v), MIN(v), MAX(v), COUNT(*)
FROM history_raw WHERE instance_id = ?2 AND item = ?3 AND field = ?4 AND ts >= ?5 AND ts < ?6
GROUP BY (ts / ?1) * ?1`,
	}))
}

// Aggregate1h 把已结束的整点桶从 5 分钟表聚合到 1 小时表（按样本数加权平均）。
// 须在同一时刻的 Aggregate5m 之后调用。幂等性与起算规则同 Aggregate5m。
func (s *Service) Aggregate1h(ctx context.Context) error {
	now := s.clk.Now().UnixMilli()
	rs := s.retention()
	return s.fail(s.aggregate(ctx, aggSpec{
		meta: metaAgg1h, dst: "history_1h", step: step1h, now: now,
		lo: ceilTo(now-int64(rs.FiveMinDays)*24*step1h, step1h),
		sel: `SELECT instance_id, item, field, (bucket / ?1) * ?1, SUM(v_avg * n) / SUM(n), MIN(v_min), MAX(v_max), SUM(n)
FROM history_5m WHERE instance_id = ?2 AND item = ?3 AND field = ?4 AND bucket >= ?5 AND bucket < ?6
GROUP BY (bucket / ?1) * ?1`,
	}))
}

type aggSpec struct {
	meta, dst, sel string
	step, now, lo  int64
}

func (s *Service) aggregate(ctx context.Context, sp aggSpec) error {
	cut := sp.now / sp.step * sp.step
	wm, err := s.metaInt(ctx, sp.meta)
	if err != nil {
		return err
	}
	from := max(sp.lo, wm-sp.step)
	if from >= cut {
		return nil
	}
	series, err := s.seriesSnapshot(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := tx.PrepareContext(ctx, `INSERT INTO `+sp.dst+` (instance_id, item, field, bucket, v_avg, v_min, v_max, n) `+sp.sel+`
ON CONFLICT (instance_id, item, field, bucket) DO UPDATE SET
    v_avg = excluded.v_avg, v_min = excluded.v_min, v_max = excluded.v_max, n = excluded.n`)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	for _, k := range series {
		if _, err := st.ExecContext(ctx, sp.step, k.id, k.item, k.field, from, cut); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		sp.meta, strconv.FormatInt(cut, 10)); err != nil {
		return err
	}
	return tx.Commit()
}

// Cleanup 按设置里的保留期清理三张表（保留期现取，改设置后下次清理生效）。
func (s *Service) Cleanup(ctx context.Context) error {
	if err := s.cleanupRaw(ctx); err != nil {
		return err
	}
	return s.cleanupAggregates(ctx)
}

// cleanupRaw 逐序列按主键范围删除过期样本：只读写要删的行，且每条语句很短，
// 不会像整表扫描那样长时间占住唯一的数据库连接。
func (s *Service) cleanupRaw(ctx context.Context) error {
	cutoff := s.clk.Now().UnixMilli() - int64(s.retention().RawHours)*step1h
	series, err := s.seriesSnapshot(ctx)
	if err != nil {
		return s.fail(err)
	}
	for _, k := range series {
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM history_raw WHERE instance_id = ? AND item = ? AND field = ? AND ts < ?`,
			k.id, k.item, k.field, cutoff); err != nil {
			return s.fail(err)
		}
	}
	s.pruneKnown(cutoff)
	return nil
}

func (s *Service) cleanupAggregates(ctx context.Context) error {
	now := s.clk.Now().UnixMilli()
	rs := s.retention()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM history_5m WHERE bucket < ?`, now-int64(rs.FiveMinDays)*24*step1h); err != nil {
		return s.fail(err)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM history_1h WHERE bucket < ?`, now-int64(rs.HourDays)*24*step1h)
	return s.fail(err)
}

// Start 启动后台循环：每个写盘间隔（默认 60 秒）写一次原始表，每 5 分钟聚合到 5 分钟表，
// 每小时再聚合到 1 小时表并清理原始表，每天清理两张聚合表（首轮会全部清理一次）。
// ctx 结束后做最后一次写盘；返回的 channel 在循环退出后关闭。
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	every := func(d time.Duration) int { return max(1, int((d+s.every/2)/s.every)) }
	n5m, n1h, n1d := every(5*time.Minute), every(time.Hour), every(24*time.Hour)
	go func() {
		defer close(done)
		bg := context.WithoutCancel(ctx)
		for tick := 1; ; tick++ {
			select {
			case <-s.clk.After(s.every):
			case <-ctx.Done():
				s.warn("停止时历史写盘失败", s.Flush(bg))
				return
			}
			s.warn("历史写盘失败", s.Flush(bg))
			if tick%n5m == 0 {
				s.warn("5 分钟聚合失败", s.Aggregate5m(bg))
			}
			if tick%n1h == 0 {
				s.warn("1 小时聚合失败", s.Aggregate1h(bg))
			}
			if tick == 1 || tick%n1h == 0 {
				s.warn("原始历史清理失败", s.cleanupRaw(bg))
			}
			if tick == 1 || tick%n1d == 0 {
				s.warn("聚合历史清理失败", s.cleanupAggregates(bg))
			}
		}
	}()
	return done
}

func (s *Service) warn(msg string, err error) {
	if err != nil {
		s.log.Warn(msg, "err", err)
	}
}
