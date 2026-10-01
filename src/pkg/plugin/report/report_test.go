package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTypesAndDefaultFields(t *testing.T) {
	want := map[string]struct {
		fields []string
		def    string
	}{
		"gauge":  {[]string{"value", "unit", "min", "max"}, "value"},
		"number": {[]string{"value", "unit"}, "value"},
		"quota":  {[]string{"used", "total", "remaining", "remaining_pct", "resets_at", "expires_at", "unit", "label"}, "remaining_pct"},
		"money":  {[]string{"amount", "currency", "used", "total"}, "amount"},
		"state":  {[]string{"state", "text"}, "state"},
		"text":   {[]string{"text"}, "text"},
		"table":  {[]string{"columns", "rows"}, ""},
	}
	if len(ItemTypes) != len(want) {
		t.Fatalf("ItemTypes 数量 = %d", len(ItemTypes))
	}
	for typ, w := range want {
		if !IsType(typ) {
			t.Errorf("IsType(%q) = false", typ)
		}
		if got := FieldsOf(typ); !reflect.DeepEqual(got, w.fields) {
			t.Errorf("FieldsOf(%s) = %v, want %v", typ, got, w.fields)
		}
		def, ok := DefaultField(typ)
		if def != w.def || ok != (w.def != "") {
			t.Errorf("DefaultField(%s) = %q,%v", typ, def, ok)
		}
		for _, f := range w.fields {
			if !ValidField(typ, f) {
				t.Errorf("ValidField(%s,%s) = false", typ, f)
			}
		}
		if ValidField(typ, "nope") {
			t.Errorf("ValidField(%s,nope) = true", typ)
		}
	}
	if IsType("chart") || FieldsOf("chart") != nil {
		t.Error("未知类型不应有定义")
	}
	if ValidField("quota", "value") {
		t.Error("quota 没有 value 字段")
	}
}

func TestFieldsOfReturnsCopy(t *testing.T) {
	f := FieldsOf("gauge")
	f[0] = "hacked"
	if FieldsOf("gauge")[0] != "value" {
		t.Error("FieldsOf 应返回副本")
	}
}

func TestResolveField(t *testing.T) {
	if f, err := ResolveField("quota", ""); err != nil || f != "remaining_pct" {
		t.Errorf("省略 field 应取默认: %q %v", f, err)
	}
	if f, err := ResolveField("quota", "used"); err != nil || f != "used" {
		t.Errorf("显式字段: %q %v", f, err)
	}
	if _, err := ResolveField("quota", "value"); err == nil {
		t.Error("非法字段应报错")
	}
	if _, err := ResolveField("table", ""); err == nil {
		t.Error("table 省略 field 解析默认字段应报错（无默认）")
	}
	if _, err := ResolveField("chart", ""); err == nil {
		t.Error("未知类型应报错")
	}
}

func TestParseKey(t *testing.T) {
	cases := []struct {
		in   string
		want Key
		bad  bool
	}{
		{in: "quota.5h", want: Key{Prefix: "quota.5h"}},
		{in: "disk[/vol1]", want: Key{Prefix: "disk", Member: "/vol1", Dynamic: true}},
		{in: "container[nginx]", want: Key{Prefix: "container", Member: "nginx", Dynamic: true}},
		{in: "disk[*]", want: Key{Prefix: "disk", Member: "*", Dynamic: true, Wildcard: true}},
		{in: "net[eth0][x]", want: Key{Prefix: "net", Member: "eth0][x", Dynamic: true}},
		{in: "", bad: true},
		{in: "[x]", bad: true},
		{in: "disk[]", bad: true},
		{in: "disk[", bad: true},
		{in: "disk[a]tail", bad: true},
	}
	for _, c := range cases {
		got, err := ParseKey(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("ParseKey(%q) 应报错", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("ParseKey(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
}

func TestKeyMatches(t *testing.T) {
	w, _ := ParseKey("disk[*]")
	if !w.Matches("disk[/vol1]") || !w.Matches("disk[/vol2]") {
		t.Error("通配应匹配同前缀成员")
	}
	if w.Matches("disk") || w.Matches("raid[md0]") || w.Matches("diskx[a]") {
		t.Error("通配不应匹配其他前缀或固定键")
	}
	k, _ := ParseKey("disk[/vol1]")
	if !k.Matches("disk[/vol1]") || k.Matches("disk[/vol2]") {
		t.Error("具体成员只匹配自身")
	}
	f, _ := ParseKey("quota.5h")
	if !f.Matches("quota.5h") || f.Matches("quota.tool") {
		t.Error("固定键精确匹配")
	}
	bare, _ := ParseKey("disk")
	if bare.Matches("disk[/vol1]") {
		t.Error("固定键 disk 不匹配动态成员")
	}
}

func sampleReport() *Report {
	f := func(v float64) *float64 { return &v }
	return &Report{
		Status: StatusOK,
		Items: []Item{
			{Key: "quota.5h", Type: "quota", RemainingPct: f(72)},
			{Key: "disk[/vol1]", Type: "gauge", Value: f(71.2)},
			{Key: "disk[/vol2]", Type: "gauge", Value: f(10)},
			{Key: "log", Type: "table"},
		},
	}
}

func TestFindSelectLookup(t *testing.T) {
	r := sampleReport()
	if it := r.Find("disk[/vol1]"); it == nil || *it.Value != 71.2 {
		t.Errorf("Find = %+v", it)
	}
	if r.Find("ghost") != nil {
		t.Error("不存在应为 nil")
	}
	got := r.Select("disk[*]")
	if len(got) != 2 || got[0].Key != "disk[/vol1]" || got[1].Key != "disk[/vol2]" {
		t.Errorf("Select 通配 = %+v", got)
	}
	if got := r.Select("quota.5h"); len(got) != 1 {
		t.Errorf("Select 精确 = %+v", got)
	}
	if got := r.Select("bad["); len(got) != 0 {
		t.Errorf("非法模式应空: %+v", got)
	}

	it, field, err := r.Lookup(Ref{Item: "quota.5h"})
	if err != nil || it == nil || field != "remaining_pct" {
		t.Errorf("省略 field 取默认: %+v %q %v", it, field, err)
	}
	if _, field, err = r.Lookup(Ref{Item: "quota.5h", Field: "used"}); err != nil || field != "used" {
		t.Errorf("显式 field: %q %v", field, err)
	}
	if it, _, err = r.Lookup(Ref{Item: "ghost"}); err != nil || it != nil {
		t.Errorf("缺失数据项应返回 nil,nil（缺失不是错误）: %+v %v", it, err)
	}
	if _, _, err = r.Lookup(Ref{Item: "quota.5h", Field: "value"}); err == nil {
		t.Error("字段不属于类型应报错")
	}
	if it, field, err = r.Lookup(Ref{Item: "log"}); err != nil || it == nil || field != "" {
		t.Errorf("table 省略 field 合法: %+v %q %v", it, field, err)
	}
}

func nopLog() *slog.Logger { return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)) }

func TestParseGoodReportFromDesign(t *testing.T) {
	src := `{
  "status": "ok", "summary": "剩余 72%", "collected_at": 1790000000123, "duration_ms": 842,
  "items": [
    {"key": "quota.5h", "type": "quota", "used": 28, "total": 100, "remaining_pct": 72, "resets_at": 1790003600000},
    {"key": "disk[/vol1]", "type": "gauge", "value": 71.2, "unit": "%", "min": 0, "max": 100},
    {"key": "raid[md0]", "type": "state", "state": "unknown", "error": "mdadm 调用失败：权限不足"}
  ],
  "events": [{"id": "e1", "type": "task", "seq": 1042, "at": 1790000000123, "tool": "codex"}],
  "state": "opaque"
}`
	r, err := Parse([]byte(src), nopLog())
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusOK || len(r.Items) != 3 || r.State != "opaque" || r.CollectedAt != 1790000000123 {
		t.Errorf("解析结果: %+v", r)
	}
	if *r.Items[0].ResetsAt != 1790003600000 || *r.Items[0].Used != 28 {
		t.Errorf("quota: %+v", r.Items[0])
	}
	if r.Items[2].Error == "" || r.Items[2].State != StatusUnknown {
		t.Errorf("自带 error 的条目应保留: %+v", r.Items[2])
	}
	if len(r.Events) != 1 || r.Events[0].ID != "e1" || r.Events[0].At != 1790000000123 {
		t.Fatalf("events: %+v", r.Events)
	}
	if string(r.Events[0].Extra["seq"]) != "1042" {
		t.Errorf("事件额外字段应保留: %v", r.Events[0].Extra)
	}
	out, err := json.Marshal(r.Events[0])
	if err != nil || !strings.Contains(string(out), `"tool":"codex"`) || !strings.Contains(string(out), `"at":1790000000123`) {
		t.Errorf("事件往返: %s %v", out, err)
	}
}

func TestParseMissingIsNotZero(t *testing.T) {
	r, err := Parse([]byte(`{"status":"ok","items":[{"key":"a","type":"gauge"},{"key":"b","type":"gauge","value":0}]}`), nopLog())
	if err != nil {
		t.Fatal(err)
	}
	if r.Items[0].Value != nil {
		t.Error("缺失的 value 应为 nil")
	}
	if r.Items[1].Value == nil || *r.Items[1].Value != 0 {
		t.Error("显式 0 应保留为 0")
	}
}

func TestParseDropsUnknownTypeAndLogs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	r, err := Parse([]byte(`{"status":"ok","items":[{"key":"a","type":"chart"},{"key":"b","type":"text","text":"hi"}]}`), log)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 1 || r.Items[0].Key != "b" {
		t.Errorf("未知 type 应被丢弃: %+v", r.Items)
	}
	if !strings.Contains(buf.String(), "chart") {
		t.Errorf("应记日志: %q", buf.String())
	}
}

func TestParseIgnoresPluginStaleFlags(t *testing.T) {
	r, err := Parse([]byte(`{"status":"ok","stale":true,"items":[{"key":"a","type":"text","stale":true}]}`), nopLog())
	if err != nil {
		t.Fatal(err)
	}
	if r.Stale || r.Items[0].Stale {
		t.Error("stale 由运行时设置，插件自报应被忽略")
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"非 JSON", `not json`, "JSON"},
		{"缺 status", `{"items":[]}`, "status"},
		{"status 非法", `{"status":"fine"}`, "status"},
		{"item 缺 key", `{"status":"ok","items":[{"type":"text"}]}`, "items[0].key"},
		{"item key 非法", `{"status":"ok","items":[{"key":"d[","type":"text"}]}`, "items[0].key"},
		{"item key 是通配", `{"status":"ok","items":[{"key":"d[*]","type":"text"}]}`, "items[0].key"},
		{"item key 重复", `{"status":"ok","items":[{"key":"a","type":"text"},{"key":"a","type":"text"}]}`, "items[1].key"},
		{"state 取值非法", `{"status":"ok","items":[{"key":"a","type":"state","state":"great"}]}`, "items[0].state"},
		{"event 缺 id", `{"status":"ok","events":[{"type":"task","at":1}]}`, "events[0].id"},
		{"event 缺 type", `{"status":"ok","events":[{"id":"x","at":1}]}`, "events[0].type"},
		{"event 缺 at", `{"status":"ok","events":[{"id":"x","type":"task"}]}`, "events[0].at"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.src), nopLog())
		if err == nil {
			t.Errorf("%s: 应报错", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: 错误 %q 应含 %q", c.name, err, c.want)
		}
	}
}

func TestParseCollectsAllProblems(t *testing.T) {
	_, err := Parse([]byte(`{"status":"bad","items":[{"type":"text"}],"events":[{}]}`), nopLog())
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("应为 *ValidationError: %v", err)
	}
	if len(ve.Problems) < 4 {
		t.Errorf("应一次收集全部问题: %v", ve.Problems)
	}
}

func TestParseNilLoggerOK(t *testing.T) {
	if _, err := Parse([]byte(`{"status":"ok","items":[{"key":"a","type":"zzz"}]}`), nil); err != nil {
		t.Fatal(err)
	}
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func TestDisplayStatePriority(t *testing.T) {
	now := time.UnixMilli(1_000_000_000)
	fresh := ms(now.Add(-time.Second))
	base := DisplayInput{LastSuccessAt: fresh, Interval: time.Minute, ReportStatus: StatusOK}
	all := func() DisplayInput {
		in := base
		in.Unconfigured, in.Broken, in.Offline, in.Maintenance, in.LastFailed = true, true, true, true, true
		in.LastSuccessAt = ms(now.Add(-time.Hour))
		in.ReportStatus = StatusCritical
		return in
	}
	steps := []struct {
		name string
		drop func(*DisplayInput)
		want DisplayState
	}{
		{"全部成立取未配置", func(*DisplayInput) {}, DisplayUnconfigured},
		{"去掉未配置取引用失效", func(in *DisplayInput) { in.Unconfigured = false }, DisplayBroken},
		{"再去掉取离线", func(in *DisplayInput) { in.Broken = false }, DisplayOffline},
		{"再去掉取维护", func(in *DisplayInput) { in.Offline = false }, DisplayMaintenance},
		{"再去掉取采集失败", func(in *DisplayInput) { in.Maintenance = false }, DisplayError},
		{"再去掉取过期", func(in *DisplayInput) { in.LastFailed = false }, DisplayStale},
		{"再去掉取报告状态", func(in *DisplayInput) { in.LastSuccessAt = fresh }, DisplayCritical},
	}
	in := all()
	for _, s := range steps {
		s.drop(&in)
		if got := ComputeDisplayState(in, now); got != s.want {
			t.Errorf("%s: got %s, want %s", s.name, got, s.want)
		}
	}
}

func TestDisplayStateFromReportStatus(t *testing.T) {
	now := time.UnixMilli(1_000_000_000)
	for st, want := range map[Status]DisplayState{
		StatusOK: DisplayOK, StatusWarning: DisplayWarning, StatusCritical: DisplayCritical, StatusUnknown: DisplayUnknown,
	} {
		in := DisplayInput{LastSuccessAt: ms(now), Interval: time.Minute, ReportStatus: st}
		if got := ComputeDisplayState(in, now); got != want {
			t.Errorf("status %s: got %s", st, got)
		}
	}
	// 从未成功过且未失败、或状态非法：未知
	if got := ComputeDisplayState(DisplayInput{Interval: time.Minute}, now); got != DisplayUnknown {
		t.Errorf("从未采集: got %s", got)
	}
	if got := ComputeDisplayState(DisplayInput{LastSuccessAt: ms(now), Interval: time.Minute, ReportStatus: "weird"}, now); got != DisplayUnknown {
		t.Errorf("非法状态: got %s", got)
	}
}

func TestErrorAndUnknownAreDistinct(t *testing.T) {
	now := time.UnixMilli(1_000_000_000)
	ok := DisplayInput{LastSuccessAt: ms(now), Interval: time.Minute}
	failed, unknown := ok, ok
	failed.LastFailed = true
	unknown.ReportStatus = StatusUnknown
	if ComputeDisplayState(failed, now) != DisplayError || ComputeDisplayState(unknown, now) != DisplayUnknown {
		t.Error("error 与 unknown 必须是两个不同状态")
	}
}

func TestStaleBoundary(t *testing.T) {
	now := time.UnixMilli(1_000_000_000)
	iv := 10 * time.Second
	at := func(age time.Duration) int64 { return ms(now.Add(-age)) }
	cases := []struct {
		name  string
		age   time.Duration
		stale bool
	}{
		{"小于 3 倍", 29 * time.Second, false},
		{"恰好 3 倍不算过期", 30 * time.Second, false},
		{"超过 3 倍 1ms", 30*time.Second + time.Millisecond, true},
	}
	for _, c := range cases {
		if got := IsStale(at(c.age), iv, 0, now); got != c.stale {
			t.Errorf("%s: IsStale = %v", c.name, got)
		}
		in := DisplayInput{LastSuccessAt: at(c.age), Interval: iv, ReportStatus: StatusOK}
		want := DisplayOK
		if c.stale {
			want = DisplayStale
		}
		if got := ComputeDisplayState(in, now); got != want {
			t.Errorf("%s: display = %s", c.name, got)
		}
	}
	if IsStale(0, iv, 0, now) {
		t.Error("从未成功不算过期（由其它状态表达）")
	}
	if IsStale(at(time.Hour), 0, 0, now) {
		t.Error("间隔与过期秒数都未给出时无法判定过期")
	}
}

func TestStaleAfterForStreamer(t *testing.T) {
	now := time.UnixMilli(1_000_000_000)
	at := func(age time.Duration) int64 { return ms(now.Add(-age)) }
	sa := 45 * time.Second
	if IsStale(at(45*time.Second), time.Second, sa, now) {
		t.Error("恰好等于声明秒数不算过期")
	}
	if !IsStale(at(45*time.Second+time.Millisecond), time.Hour, sa, now) {
		t.Error("超过声明秒数即过期，且忽略间隔")
	}
	if IsStale(at(2*time.Minute), 0, 5*time.Minute, now) == true {
		t.Error("声明 5 分钟内不过期")
	}
}

func TestMergeSuccessReplaces(t *testing.T) {
	prev := sampleReport()
	res := &Report{Status: StatusWarning, Items: []Item{{Key: "only", Type: "text", Text: "x"}}}
	cur, stale := Merge(prev, res, nil)
	if stale || cur != res {
		t.Errorf("成功应整份替换且不过期: %+v %v", cur, stale)
	}
	if len(cur.Items) != 1 {
		t.Error("全量快照：缺失的数据项视为已移除")
	}
}

func TestMergeFailureKeepsPrevMarkedStale(t *testing.T) {
	prev := sampleReport()
	prev.State = "cursor"
	prev.Summary = "旧摘要"
	cur, stale := Merge(prev, nil, errors.New("boom"))
	if !stale || cur == nil || !cur.Stale {
		t.Fatalf("应保留旧值并标记过期: %+v %v", cur, stale)
	}
	if len(cur.Items) != len(prev.Items) || cur.Status != StatusOK || cur.Summary != "旧摘要" || cur.State != "cursor" {
		t.Errorf("旧内容应保留: %+v", cur)
	}
	for _, it := range cur.Items {
		if !it.Stale {
			t.Errorf("每个数据项应标记过期: %+v", it)
		}
	}
	if *cur.Items[1].Value != 71.2 {
		t.Error("旧值不得被改写或清零")
	}
	if prev.Items[0].Stale || prev.Stale {
		t.Error("不得修改入参 prev")
	}
}

func TestMergeFailureWithoutPrev(t *testing.T) {
	cur, stale := Merge(nil, nil, errors.New("boom"))
	if cur != nil || stale {
		t.Errorf("无旧值时无可展示数据，不得伪造: %+v %v", cur, stale)
	}
}

func TestMergeFailureIgnoresPartialResult(t *testing.T) {
	prev := sampleReport()
	cur, stale := Merge(prev, &Report{Status: StatusCritical}, errors.New("boom"))
	if !stale || cur.Status != StatusOK {
		t.Errorf("出错时忽略本次结果，沿用旧值: %+v", cur)
	}
}

func TestMergeRepeatedFailureStaysStale(t *testing.T) {
	cur, _ := Merge(sampleReport(), nil, errors.New("1"))
	cur2, stale := Merge(cur, nil, errors.New("2"))
	if !stale || !cur2.Stale || len(cur2.Items) != 4 {
		t.Errorf("连续失败仍保留并标记: %+v", cur2)
	}
}
