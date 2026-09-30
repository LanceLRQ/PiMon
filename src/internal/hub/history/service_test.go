package history

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type fx struct {
	t    *testing.T
	db   *store.DB
	clk  *clock.Fake
	ret  model.RetentionSettings
	svc  *Service
	conf Config
}

func newFx(t *testing.T) *fx {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &fx{t: t, db: db, clk: clock.NewFake(t0), ret: model.RetentionSettings{RawHours: 24, FiveMinDays: 30, HourDays: 365}}
	f.conf = Config{DB: db, Clock: f.clk, Retention: func() model.RetentionSettings { return f.ret }}
	f.svc = New(f.conf)
	f.addInstance("i1")
	return f
}

func (f *fx) addInstance(id string) {
	f.t.Helper()
	_, err := f.db.Exec(`INSERT INTO plugin_instances (id, plugin_id, name, config_json, config_hash, created_at, updated_at)
VALUES (?, 'p', 'n', '{}', 'h', 't', 't')`, id)
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) count(table string) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func fp(v float64) *float64 { return &v }

func numRep(key string, v float64) *report.Report {
	return &report.Report{Status: report.StatusOK, Items: []report.Item{{Key: key, Type: report.TypeNumber, Value: &v}}}
}

func (f *fx) rec(id, key string, at time.Time, v float64) {
	f.svc.Record(id, at, numRep(key, v))
}

func (f *fx) flush() {
	f.t.Helper()
	if err := f.svc.Flush(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

type aggRow struct {
	bucket          int64
	avg, vmin, vmax float64
	n               int
}

func (f *fx) aggRows(table, item string) []aggRow {
	f.t.Helper()
	rows, err := f.db.Query(`SELECT bucket, v_avg, v_min, v_max, n FROM `+table+` WHERE item = ? ORDER BY bucket`, item)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []aggRow
	for rows.Next() {
		var r aggRow
		if err := rows.Scan(&r.bucket, &r.avg, &r.vmin, &r.vmax, &r.n); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRecordNumericFieldsOnly(t *testing.T) {
	f := newFx(t)
	pct, used := 40.0, 60.0
	bad := math.NaN()
	rep := &report.Report{Status: report.StatusOK, Items: []report.Item{
		{Key: "g", Type: report.TypeGauge, Value: fp(1.5)},
		{Key: "n", Type: report.TypeNumber, Value: fp(2)},
		{Key: "q", Type: report.TypeQuota, RemainingPct: &pct, Used: &used, Total: fp(100)},
		{Key: "m", Type: report.TypeMoney, Amount: fp(9.9), Currency: "CNY"},
		{Key: "s", Type: report.TypeState, State: report.StatusOK},
		{Key: "t", Type: report.TypeText, Text: "x"},
		{Key: "tb", Type: report.TypeTable, Columns: []string{"a"}},
		{Key: "missing", Type: report.TypeGauge},                         // 数值缺失：不记，不当作 0
		{Key: "err", Type: report.TypeNumber, Error: "读取失败"},             // 带 error 且缺失：不记
		{Key: "q2", Type: report.TypeQuota, Used: fp(3)},                 // 仅 used：只记 used
		{Key: "nan", Type: report.TypeNumber, Value: &bad},               // 非有限值不记
		{Key: "old", Type: report.TypeNumber, Value: fp(7), Stale: true}, // 保留的旧值不当作新采样
		{Key: "disk[/vol1]", Type: report.TypeGauge, Value: fp(55)},      // 动态成员自动进入
	}}
	f.svc.Record("i1", t0, rep)
	f.flush()

	rows, err := f.db.Query(`SELECT item, field, ts, v FROM history_raw ORDER BY item, field`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]float64{}
	for rows.Next() {
		var item, field string
		var ts int64
		var v float64
		if err := rows.Scan(&item, &field, &ts, &v); err != nil {
			t.Fatal(err)
		}
		if ts != t0.UnixMilli() {
			t.Fatalf("ts = %d", ts)
		}
		got[item+"/"+field] = v
	}
	want := map[string]float64{
		"g/value": 1.5, "n/value": 2, "q/remaining_pct": 40, "q/used": 60,
		"m/amount": 9.9, "q2/used": 3, "disk[/vol1]/value": 55,
	}
	if len(got) != len(want) {
		t.Fatalf("got = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, want %v (got=%v)", k, got[k], v, got)
		}
	}
}

func TestRecordedFieldsFollowReportPackage(t *testing.T) {
	for _, typ := range []string{report.TypeGauge, report.TypeNumber, report.TypeQuota, report.TypeMoney} {
		def, ok := report.DefaultField(typ)
		if !ok {
			t.Fatalf("%s 没有默认字段", typ)
		}
		fs := recordedFields[typ]
		if len(fs) == 0 || fs[0] != def {
			t.Fatalf("%s 记录字段 = %v，首个应为默认字段 %s", typ, fs, def)
		}
		for _, x := range fs {
			if !report.ValidField(typ, x) {
				t.Fatalf("%s 的字段 %s 不在 report 字段集内", typ, x)
			}
		}
	}
}

func TestFlushLoopWritesEverySixtySeconds(t *testing.T) {
	f := newFx(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := f.svc.Start(ctx)
	waitFor(t, "循环就绪", func() bool { return f.clk.Waiters() >= 1 })
	f.rec("i1", "temp", f.clk.Now(), 20)
	if f.count("history_raw") != 0 {
		t.Fatal("未到 60 秒不应写盘")
	}
	f.clk.Advance(60 * time.Second)
	waitFor(t, "raw 有数据", func() bool { return f.count("history_raw") == 1 })

	// 关停时最后写一次
	waitFor(t, "循环重新就绪", func() bool { return f.clk.Waiters() >= 1 })
	f.rec("i1", "temp", f.clk.Now().Add(time.Second), 21)
	cancel()
	<-done
	if f.count("history_raw") != 2 {
		t.Fatalf("关停最后写盘后 raw = %d", f.count("history_raw"))
	}
}

func TestLoopAggregatesOnSchedule(t *testing.T) {
	f := newFx(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := f.svc.Start(ctx)
	defer func() { cancel(); <-done }()
	f.rec("i1", "temp", t0.Add(time.Minute), 10)
	for i := 0; i < 6; i++ {
		waitFor(t, "循环就绪", func() bool { return f.clk.Waiters() >= 1 })
		f.clk.Advance(60 * time.Second)
		waitFor(t, "本轮处理完", func() bool { return f.clk.Waiters() >= 1 })
	}
	waitFor(t, "5m 聚合出现", func() bool { return len(f.aggRows("history_5m", "temp")) == 1 })
}

func TestAggregate5mBucketsAndIdempotent(t *testing.T) {
	f := newFx(t)
	// 桶 [0,5m)：10、20、30；桶 [5m,10m)：40；未结束的桶 [10m,15m)：100
	f.rec("i1", "temp", t0.Add(0), 10)
	f.rec("i1", "temp", t0.Add(2*time.Minute), 20)
	f.rec("i1", "temp", t0.Add(4*time.Minute+59*time.Second), 30)
	f.rec("i1", "temp", t0.Add(5*time.Minute), 40)
	f.rec("i1", "temp", t0.Add(11*time.Minute), 100)
	f.flush()

	f.clk.Advance(12 * time.Minute)
	if err := f.svc.Aggregate5m(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows := f.aggRows("history_5m", "temp")
	if len(rows) != 2 {
		t.Fatalf("只应聚合已结束的桶: %+v", rows)
	}
	b0, b1 := rows[0], rows[1]
	if b0.bucket != t0.UnixMilli() || !near(b0.avg, 20) || b0.vmin != 10 || b0.vmax != 30 || b0.n != 3 {
		t.Fatalf("桶0 = %+v", b0)
	}
	if b1.bucket != t0.Add(5*time.Minute).UnixMilli() || b1.avg != 40 || b1.vmin != 40 || b1.vmax != 40 || b1.n != 1 {
		t.Fatalf("桶1 = %+v", b1)
	}

	// 重复执行幂等；重启（新实例）后重跑同样不产生重复行或改变值
	if err := f.svc.Aggregate5m(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := New(f.conf)
	if err := restarted.Aggregate5m(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := f.aggRows("history_5m", "temp")
	if len(again) != 2 || again[0] != b0 || again[1] != b1 {
		t.Fatalf("重跑后 = %+v", again)
	}

	// 时间推进后，第三个桶在结束后被聚合，前两个不变
	f.clk.Advance(5 * time.Minute)
	if err := f.svc.Aggregate5m(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows = f.aggRows("history_5m", "temp")
	if len(rows) != 3 || rows[2].avg != 100 || rows[0] != b0 {
		t.Fatalf("推进后 = %+v", rows)
	}
}

func TestAggregate5mCatchUpAfterDowntime(t *testing.T) {
	f := newFx(t)
	f.rec("i1", "temp", t0.Add(time.Minute), 1)
	f.flush()
	f.clk.Advance(10 * time.Minute)
	if err := f.svc.Aggregate5m(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 之后停机 3 小时，其间没有新数据；再来新样本并聚合，追上后不丢桶
	f.clk.Advance(3 * time.Hour)
	f.rec("i1", "temp", f.clk.Now().Add(-time.Minute), 2)
	f.flush()
	f.clk.Advance(10 * time.Minute)
	restarted := New(f.conf)
	if err := restarted.Aggregate5m(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rows := f.aggRows("history_5m", "temp"); len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestAggregate1hWeightedAndOnlyEndedBuckets(t *testing.T) {
	f := newFx(t)
	// 第一个小时：前 5 分钟桶 3 个样本（10、20、30），第二个桶 1 个样本（70）；加权平均 = (60+70)/4
	f.rec("i1", "temp", t0.Add(0), 10)
	f.rec("i1", "temp", t0.Add(time.Minute), 20)
	f.rec("i1", "temp", t0.Add(2*time.Minute), 30)
	f.rec("i1", "temp", t0.Add(7*time.Minute), 70)
	// 第二个小时（未结束）
	f.rec("i1", "temp", t0.Add(61*time.Minute), 500)
	f.flush()

	f.clk.Advance(90 * time.Minute)
	ctx := context.Background()
	if err := f.svc.Aggregate5m(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Aggregate1h(ctx); err != nil {
		t.Fatal(err)
	}
	rows := f.aggRows("history_1h", "temp")
	if len(rows) != 1 {
		t.Fatalf("只应聚合已结束的小时: %+v", rows)
	}
	r := rows[0]
	if r.bucket != t0.UnixMilli() || !near(r.avg, 32.5) || r.vmin != 10 || r.vmax != 70 || r.n != 4 {
		t.Fatalf("1h = %+v", r)
	}
	// 幂等
	if err := f.svc.Aggregate1h(ctx); err != nil {
		t.Fatal(err)
	}
	if err := New(f.conf).Aggregate1h(ctx); err != nil {
		t.Fatal(err)
	}
	if again := f.aggRows("history_1h", "temp"); len(again) != 1 || again[0] != r {
		t.Fatalf("重跑后 = %+v", again)
	}
}

func TestCleanupUsesCurrentRetention(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	f.clk.Advance(400 * 24 * time.Hour)
	now := f.clk.Now()
	ins := func(table string, age time.Duration) {
		var err error
		if table == "history_raw" {
			_, err = f.db.Exec(`INSERT INTO history_raw VALUES ('i1','temp','value',?,1)`, now.Add(-age).UnixMilli())
		} else {
			_, err = f.db.Exec(`INSERT INTO `+table+` VALUES ('i1','temp','value',?,1,1,1,1)`, now.Add(-age).UnixMilli())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, age := range []time.Duration{time.Hour, 23 * time.Hour, 25 * time.Hour, 10 * 24 * time.Hour} {
		ins("history_raw", age)
	}
	for _, age := range []time.Duration{time.Hour, 29 * 24 * time.Hour, 31 * 24 * time.Hour, 200 * 24 * time.Hour} {
		ins("history_5m", age)
	}
	for _, age := range []time.Duration{time.Hour, 300 * 24 * time.Hour, 364 * 24 * time.Hour, 366 * 24 * time.Hour} {
		ins("history_1h", age)
	}
	if err := f.svc.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.count("history_raw"); n != 2 {
		t.Fatalf("raw = %d", n)
	}
	if n := f.count("history_5m"); n != 2 {
		t.Fatalf("5m = %d", n)
	}
	if n := f.count("history_1h"); n != 3 {
		t.Fatalf("1h = %d", n)
	}
	// 改设置后下次清理按新值
	f.ret = model.RetentionSettings{RawHours: 2, FiveMinDays: 1, HourDays: 301}
	if err := f.svc.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count("history_raw") != 1 || f.count("history_5m") != 1 || f.count("history_1h") != 2 {
		t.Fatalf("新保留期后 raw=%d 5m=%d 1h=%d", f.count("history_raw"), f.count("history_5m"), f.count("history_1h"))
	}
}

func TestCascadeDeleteWithInstance(t *testing.T) {
	f := newFx(t)
	f.addInstance("i2")
	f.rec("i1", "temp", t0, 1)
	f.rec("i2", "temp", t0, 2)
	f.flush()
	for _, tb := range []string{"history_5m", "history_1h"} {
		if _, err := f.db.Exec(`INSERT INTO ` + tb + ` VALUES ('i1','temp','value',0,1,1,1,1),('i2','temp','value',0,1,1,1,1)`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`DELETE FROM plugin_instances WHERE id = 'i1'`); err != nil {
		t.Fatal(err)
	}
	for _, tb := range []string{"history_raw", "history_5m", "history_1h"} {
		if n := f.count(tb); n != 1 {
			t.Fatalf("%s 剩 %d 行，应只剩 i2 的 1 行", tb, n)
		}
	}
}

func TestFlushSkipsDeletedInstance(t *testing.T) {
	f := newFx(t)
	f.addInstance("gone")
	f.rec("gone", "temp", t0, 1)
	f.rec("i1", "temp", t0, 2)
	if _, err := f.db.Exec(`DELETE FROM plugin_instances WHERE id = 'gone'`); err != nil {
		t.Fatal(err)
	}
	f.flush() // 已删实例的样本不能让整批失败
	if f.count("history_raw") != 1 || f.svc.WriteErrors() != 0 {
		t.Fatalf("raw=%d errs=%d", f.count("history_raw"), f.svc.WriteErrors())
	}
}

func TestWriteFailureCountedAndRetried(t *testing.T) {
	f := newFx(t)
	f.rec("i1", "temp", t0, 1)
	if _, err := f.db.Exec(`ALTER TABLE history_raw RENAME TO history_raw_off`); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Flush(context.Background()); err == nil {
		t.Fatal("表不可用时 Flush 应报错")
	}
	if f.svc.WriteErrors() != 1 {
		t.Fatalf("WriteErrors = %d", f.svc.WriteErrors())
	}
	f.rec("i1", "temp", t0.Add(time.Second), 2)
	if _, err := f.db.Exec(`ALTER TABLE history_raw_off RENAME TO history_raw`); err != nil {
		t.Fatal(err)
	}
	f.flush() // 失败的批次保留，恢复后一并写入
	if f.count("history_raw") != 2 {
		t.Fatalf("恢复后 raw = %d", f.count("history_raw"))
	}
}

func TestBufferCapDropsOldest(t *testing.T) {
	f := newFx(t)
	f.conf.MaxBuffered = 3
	f.svc = New(f.conf)
	if _, err := f.db.Exec(`ALTER TABLE history_raw RENAME TO history_raw_off`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		f.rec("i1", "temp", t0.Add(time.Duration(i)*time.Second), float64(i))
	}
	// 写盘失败：批次放回缓冲，超限丢弃最旧并计数
	_ = f.svc.Flush(context.Background())
	if f.svc.Dropped() != 2 {
		t.Fatalf("Dropped = %d", f.svc.Dropped())
	}
	if _, err := f.db.Exec(`ALTER TABLE history_raw_off RENAME TO history_raw`); err != nil {
		t.Fatal(err)
	}
	f.flush()
	var minV float64
	if err := f.db.QueryRow(`SELECT MIN(v) FROM history_raw`).Scan(&minV); err != nil {
		t.Fatal(err)
	}
	if f.count("history_raw") != 3 || minV != 2 {
		t.Fatalf("raw=%d min=%v，应保留最新 3 条", f.count("history_raw"), minV)
	}
}

func TestParseRange(t *testing.T) {
	ok := map[string]time.Duration{
		"30m": 30 * time.Minute, "1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "365d": 365 * 24 * time.Hour,
	}
	for s, want := range ok {
		got, err := ParseRange(s)
		if err != nil || got != want {
			t.Fatalf("ParseRange(%q) = %v, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "h", "0h", "-1h", "1.5h", "1w", "abc", "1 h", "99999999999999d"} {
		if _, err := ParseRange(s); err == nil {
			t.Fatalf("ParseRange(%q) 应报错", s)
		}
	}
}

func (f *fx) query(item, field, rng string) (model.HistoryResult, error) {
	return f.svc.Query(context.Background(), Query{InstanceID: "i1", Item: item, Field: field, Range: rng})
}

func TestQueryPicksTierByRange(t *testing.T) {
	f := newFx(t)
	f.clk.Advance(30 * 24 * time.Hour)
	now := f.clk.Now()
	for _, r := range []struct {
		sql string
		ts  int64
	}{
		{`INSERT INTO history_raw VALUES ('i1','temp','value',?,5)`, now.Add(-30 * time.Minute).UnixMilli()},
		{`INSERT INTO history_5m VALUES ('i1','temp','value',?,6,1,9,3)`, now.Add(-2 * time.Hour).Truncate(5 * time.Minute).UnixMilli()},
		{`INSERT INTO history_1h VALUES ('i1','temp','value',?,7,2,8,12)`, now.Add(-100 * 24 * time.Hour).Truncate(time.Hour).UnixMilli()},
	} {
		if _, err := f.db.Exec(r.sql, r.ts); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		rng, tier string
		n         int
	}{
		{"1h", "raw", 1}, {"24h", "raw", 1}, {"25h", "5m", 1}, {"7d", "5m", 1}, {"30d", "5m", 1},
		{"31d", "1h", 0}, {"365d", "1h", 1},
	}
	for _, c := range cases {
		res, err := f.query("temp", "", c.rng)
		if err != nil {
			t.Fatalf("%s: %v", c.rng, err)
		}
		if res.Tier != c.tier || len(res.Points) != c.n || res.Field != "value" || res.Range != c.rng || res.InstanceID != "i1" {
			t.Fatalf("%s: tier=%s points=%d field=%s", c.rng, res.Tier, len(res.Points), res.Field)
		}
		if res.To-res.From != mustRange(t, c.rng).Milliseconds() {
			t.Fatalf("%s: 窗口 %d..%d", c.rng, res.From, res.To)
		}
	}
	raw, _ := f.query("temp", "", "1h")
	if p := raw.Points[0]; p.Avg != 5 || p.Min != 5 || p.Max != 5 {
		t.Fatalf("raw 点应三值相等: %+v", p)
	}
	m5, _ := f.query("temp", "", "7d")
	if p := m5.Points[0]; p.Avg != 6 || p.Min != 1 || p.Max != 9 {
		t.Fatalf("5m 点 = %+v", p)
	}
	// 窗口外的数据不返回
	if res, _ := f.query("temp", "", "1h"); len(res.Points) != 1 {
		t.Fatal("1h 内应只有 raw 一点")
	}

	// 保留期改变后档位随之变化：raw 只留 2 小时，则 3h 用 5m
	f.ret.RawHours = 2
	if res, _ := f.query("temp", "", "3h"); res.Tier != "5m" {
		t.Fatalf("tier = %s", res.Tier)
	}
}

func mustRange(t *testing.T, s string) time.Duration {
	t.Helper()
	d, err := ParseRange(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestQueryValidation(t *testing.T) {
	f := newFx(t)
	for _, rng := range []string{"", "abc", "0h", "366d", "9999d"} {
		_, err := f.query("temp", "", rng)
		var fe model.FieldErrors
		if !errors.As(err, &fe) || fe["range"] == "" {
			t.Fatalf("range=%q err=%v，应为 range 字段错误", rng, err)
		}
	}
	var fe model.FieldErrors
	if _, err := f.query("", "", "1h"); !errors.As(err, &fe) || fe["item"] != model.FieldRequired {
		t.Fatalf("item 缺失 err=%v", err)
	}
	if _, err := f.query("temp", "unit", "1h"); !errors.As(err, &fe) || fe["field"] == "" {
		t.Fatalf("非数值字段 err=%v", err)
	}
	// 设置里 1h 档保留期缩短后，原本合法的 range 变为超出
	f.ret.HourDays = 100
	if _, err := f.query("temp", "", "101d"); !errors.As(err, &fe) || fe["range"] != model.FieldOutOfRange {
		t.Fatalf("超出保留期 err=%v", err)
	}
	if _, err := f.svc.Query(context.Background(), Query{InstanceID: "nope", Item: "temp", Range: "1h"}); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("实例不存在 err=%v", err)
	}
}

func TestQueryFieldResolution(t *testing.T) {
	f := newFx(t)
	pct, used := 40.0, 60.0
	f.svc.Record("i1", f.clk.Now(), &report.Report{Status: report.StatusOK, Items: []report.Item{
		{Key: "quota.5h", Type: report.TypeQuota, RemainingPct: &pct, Used: &used},
		{Key: "cost", Type: report.TypeMoney, Amount: fp(3.5)},
	}})
	f.flush()
	res, err := f.query("quota.5h", "", "1h")
	if err != nil || res.Field != "remaining_pct" || len(res.Points) != 1 || res.Points[0].Avg != 40 {
		t.Fatalf("默认字段 = %+v %v", res, err)
	}
	res, err = f.query("quota.5h", "used", "1h")
	if err != nil || res.Field != "used" || res.Points[0].Avg != 60 {
		t.Fatalf("used = %+v %v", res, err)
	}
	if res, _ := f.query("cost", "", "1h"); res.Field != "amount" {
		t.Fatalf("money 默认字段 = %s", res.Field)
	}
	// 没有历史的数据项：空点列，不报错
	res, err = f.query("nothing", "", "1h")
	if err != nil || len(res.Points) != 0 || res.Points == nil {
		t.Fatalf("空结果应为非 nil 空数组: %+v %v", res, err)
	}
}

func TestAggregateCleanupUsesBucketIndex(t *testing.T) {
	f := newFx(t)
	for _, q := range []string{delete5mSQL, delete1hSQL} {
		rows, err := f.db.Query(`EXPLAIN QUERY PLAN `+q, 0)
		if err != nil {
			t.Fatal(err)
		}
		var plan string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail + "\n"
		}
		_ = rows.Close()
		if !strings.Contains(plan, "USING INDEX") && !strings.Contains(plan, "USING COVERING INDEX") {
			t.Fatalf("清理语句应走 bucket 索引: %s => %s", q, plan)
		}
	}
}

func TestParseRangeOverflow(t *testing.T) {
	for _, s := range []string{"106752d", "999999d"} {
		if _, err := ParseRange(s); !errors.Is(err, ErrRangeTooLarge) {
			t.Fatalf("ParseRange(%q) err=%v，应为 ErrRangeTooLarge", s, err)
		}
		f := newFx(t)
		var fe model.FieldErrors
		if _, err := f.query("temp", "", s); !errors.As(err, &fe) || fe["range"] != model.FieldOutOfRange {
			t.Fatalf("Query range=%s err=%v，应为 out_of_range", s, err)
		}
	}
}

func TestFirstRoundAggregatesBeforeCleanupAfterLongDowntime(t *testing.T) {
	f := newFx(t)
	ctx := context.Background()
	f.rec("i1", "temp", t0.Add(11*time.Minute), 7)
	f.flush()
	f.clk.Advance(12 * time.Minute)
	if err := f.svc.Aggregate5m(ctx); err != nil { // 写入水位线
		t.Fatal(err)
	}
	// 停机 30 小时（超过 raw 保留期 24 小时），停机前最后一段 raw 尚未聚合
	f.rec("i1", "temp", f.clk.Now().Add(-30*time.Second), 9)
	f.flush()
	f.clk.Advance(30 * time.Hour)

	restarted := New(f.conf)
	loopCtx, cancel := context.WithCancel(ctx)
	done := restarted.Start(loopCtx)
	defer func() { cancel(); <-done }()
	waitFor(t, "循环就绪", func() bool { return f.clk.Waiters() >= 1 })
	f.clk.Advance(60 * time.Second)
	waitFor(t, "首轮清理完成", func() bool { return f.count("history_raw") == 0 })
	rows := f.aggRows("history_5m", "temp")
	if len(rows) != 1 || rows[0].n != 2 || rows[0].avg != 8 {
		t.Fatalf("停机前最后一段应已聚合进 5m: %+v", rows)
	}
}
