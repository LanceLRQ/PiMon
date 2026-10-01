package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy/proxytest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
)

const forecastJSON = `{
 "latitude": 39.89, "longitude": 116.36, "timezone": "Asia/Shanghai",
 "current": {"time": "2026-09-30T20:00", "interval": 900, "temperature_2m": 17.8, "relative_humidity_2m": 11,
   "wind_speed_10m": 13.2, "wind_direction_10m": 314, "weather_code": 0, "is_day": 0},
 "daily": {"time": ["2026-09-30","2026-10-01","2026-10-02"], "weather_code": [3, 61, 3],
   "temperature_2m_max": [19.7, 22.4, 22.5], "temperature_2m_min": [14.9, 10.6, 12.0]}
}`

// fakeAPI 同时扮演预报、地理编码与 IP 定位三个服务，并记录各路径的请求数与最后一次查询参数。
type fakeAPI struct {
	srv      *httptest.Server
	forecast atomic.Int32
	search   atomic.Int32
	locate   atomic.Int32
	lastQ    atomic.Value // url.Values of latest request
	fcBody   atomic.Value
	geoBody  atomic.Value
	ipBody   atomic.Value
	ipStatus atomic.Int32
}

func newFake(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.fcBody.Store(forecastJSON)
	f.geoBody.Store(`{}`)
	f.ipBody.Store(`{"success": true, "city": "Beijing", "country": "China", "latitude": 39.9, "longitude": 116.4, "timezone": {"id": "Asia/Shanghai"}}`)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/forecast", func(w http.ResponseWriter, r *http.Request) {
		f.forecast.Add(1)
		f.lastQ.Store(r.URL.Query())
		_, _ = fmt.Fprint(w, f.fcBody.Load())
	})
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		f.search.Add(1)
		f.lastQ.Store(r.URL.Query())
		_, _ = fmt.Fprint(w, f.geoBody.Load())
	})
	mux.HandleFunc("/ip/", func(w http.ResponseWriter, r *http.Request) {
		f.locate.Add(1)
		if st := int(f.ipStatus.Load()); st != 0 {
			w.WriteHeader(st)
			return
		}
		_, _ = fmt.Fprint(w, f.ipBody.Load())
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) plugin() *plugin {
	return &plugin{m: mustManifest(), forecastBase: f.srv.URL, geocodeBase: f.srv.URL, ipBase: f.srv.URL + "/ip/"}
}

const beijing = `{"id":1,"name":"北京","lat":39.9,"lon":116.4,"tz":"Asia/Shanghai"}`

func run(t *testing.T, p *plugin, cfg map[string]any, pr *proxy.Proxy, clk clock.Clock, last *report.Report, state string) (*report.Report, error) {
	t.Helper()
	if clk == nil {
		clk = clock.NewFake(time.Unix(1_800_000_000, 0))
	}
	return p.Collect(context.Background(), runtime.Input{Config: cfg, Proxy: pr, Clock: clk, Last: last, State: state})
}

func text(t *testing.T, rep *report.Report, key string) string {
	t.Helper()
	it := rep.Find(key)
	if it == nil {
		t.Fatalf("缺少数据项 %s", key)
	}
	return it.Text
}

func num(t *testing.T, rep *report.Report, key string) float64 {
	t.Helper()
	it := rep.Find(key)
	if it == nil || it.Value == nil {
		t.Fatalf("缺少数值项 %s", key)
	}
	return *it.Value
}

func assertValid(t *testing.T, p *plugin, rep *report.Report) {
	t.Helper()
	if err := rep.Validate(nil); err != nil {
		t.Fatalf("报告应通过 Validate: %v", err)
	}
	for _, it := range rep.Items {
		ok := false
		for _, o := range p.Manifest().Outputs {
			if o.Key == it.Key {
				ok = o.Type == it.Type
				break
			}
		}
		if !ok {
			t.Errorf("键 %s（%s）不在 manifest outputs 内或类型不符", it.Key, it.Type)
		}
	}
}

func TestRegistered(t *testing.T) {
	s, ok := runtime.Builtin("weather")
	if !ok {
		t.Fatal("weather 应在 init 中注册")
	}
	if _, ok := s.(runtime.Lookuper); !ok {
		t.Fatal("weather 应实现 Lookuper")
	}
}

func TestCollectParsesForecast(t *testing.T) {
	f := newFake(t)
	p := f.plugin()
	rep, err := run(t, p, map[string]any{"city": beijing}, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != report.StatusOK {
		t.Fatalf("应 ok: %s", rep.Status)
	}
	if v := num(t, rep, "temperature"); v != 17.8 {
		t.Errorf("temperature=%v", v)
	}
	if v := num(t, rep, "humidity"); v != 11 {
		t.Errorf("humidity=%v", v)
	}
	if v := num(t, rep, "wind_speed"); v != 13.2 {
		t.Errorf("wind_speed=%v", v)
	}
	if v := num(t, rep, "tomorrow_high"); v != 22.4 {
		t.Errorf("tomorrow_high=%v", v)
	}
	if v := num(t, rep, "tomorrow_low"); v != 10.6 {
		t.Errorf("tomorrow_low=%v", v)
	}
	// is_day=0 的晴天使用夜间图标；明日（61 小雨）按白天。
	if s := text(t, rep, "icon"); s != "clear-night" {
		t.Errorf("icon=%q", s)
	}
	if s := text(t, rep, "tomorrow_icon"); s != "rain" {
		t.Errorf("tomorrow_icon=%q", s)
	}
	if s := text(t, rep, "condition"); s != "晴 / Clear sky" {
		t.Errorf("condition=%q", s)
	}
	if s := text(t, rep, "tomorrow_condition"); s != "小雨 / Slight rain" {
		t.Errorf("tomorrow_condition=%q", s)
	}
	if s := text(t, rep, "location"); s != "北京" {
		t.Errorf("location=%q", s)
	}
	attr := text(t, rep, "attribution")
	for _, want := range []string{"Weather data by Open-Meteo.com", "https://open-meteo.com/", "CC BY 4.0", "https://creativecommons.org/licenses/by/4.0/"} {
		if !strings.Contains(attr, want) {
			t.Errorf("署名缺少 %q: %s", want, attr)
		}
	}
	if !strings.Contains(rep.Summary, "北京") {
		t.Errorf("summary 应含地名: %q", rep.Summary)
	}
	q := f.lastQ.Load().(interface{ Get(string) string })
	if q.Get("latitude") != "39.9" || q.Get("longitude") != "116.4" || q.Get("timezone") != "auto" {
		t.Errorf("预报请求参数不对: %v", q)
	}
	assertValid(t, p, rep)
}

func TestNullValuesAreOmitted(t *testing.T) {
	f := newFake(t)
	f.fcBody.Store(`{"current":{"temperature_2m":null,"relative_humidity_2m":50,"wind_speed_10m":null,"weather_code":null,"is_day":1},
	  "daily":{"weather_code":[0],"temperature_2m_max":[1],"temperature_2m_min":[0]}}`)
	p := f.plugin()
	rep, err := run(t, p, map[string]any{"city": beijing}, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Find("temperature") != nil || rep.Find("wind_speed") != nil || rep.Find("condition") != nil {
		t.Errorf("缺测字段不应输出（不当作零）: %+v", rep.Items)
	}
	if rep.Find("tomorrow_high") != nil {
		t.Error("只有一天数据时不应输出明日")
	}
	if num(t, rep, "humidity") != 50 {
		t.Error("有值的字段应保留")
	}
	assertValid(t, p, rep)
}

func TestUnknownWeatherCode(t *testing.T) {
	f := newFake(t)
	f.fcBody.Store(`{"current":{"temperature_2m":5,"weather_code":97,"is_day":1},"daily":{}}`)
	rep, err := run(t, f.plugin(), map[string]any{"city": beijing}, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if s := text(t, rep, "icon"); s != "unknown" {
		t.Errorf("未知代码图标应为 unknown: %q", s)
	}
	if s := text(t, rep, "condition"); s != "未知 / Unknown" {
		t.Errorf("condition=%q", s)
	}
}

func TestWeatherCodeTableComplete(t *testing.T) {
	codes := []int{0, 1, 2, 3, 45, 48, 51, 53, 55, 56, 57, 61, 63, 65, 66, 67, 71, 73, 75, 77, 80, 81, 82, 85, 86, 95, 96, 99}
	for _, c := range codes {
		if _, ok := wmo[c]; !ok {
			t.Errorf("缺少 WMO 代码 %d", c)
		}
	}
	if len(wmo) != len(codes) {
		t.Errorf("WMO 表应恰有 %d 项: %d", len(codes), len(wmo))
	}
}

func TestForecastHTTPErrorHidesURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }))
	t.Cleanup(srv.Close)
	p := &plugin{m: mustManifest(), forecastBase: srv.URL}
	_, err := run(t, p, map[string]any{"city": beijing}, nil, nil, nil, "")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("应报 HTTP 429: %v", err)
	}
	if strings.Contains(err.Error(), "latitude") {
		t.Errorf("错误不应含完整 URL: %v", err)
	}
	// 连接失败同样不含 URL。
	p2 := &plugin{m: mustManifest(), forecastBase: "http://127.0.0.1:1"}
	_, err = run(t, p2, map[string]any{"city": beijing}, nil, nil, nil, "")
	if err == nil || strings.Contains(err.Error(), "latitude") {
		t.Errorf("连接错误不应含查询串: %v", err)
	}
}

func TestOversizedResponseRejected(t *testing.T) {
	f := newFake(t)
	f.fcBody.Store(`{"pad":"` + strings.Repeat("x", maxBody+10) + `"}`)
	if _, err := run(t, f.plugin(), map[string]any{"city": beijing}, nil, nil, nil, ""); err == nil {
		t.Fatal("超过大小上限应报错")
	}
}

func TestConfigErrors(t *testing.T) {
	f := newFake(t)
	p := f.plugin()
	if _, err := run(t, p, map[string]any{}, nil, nil, nil, ""); err == nil || !strings.Contains(err.Error(), "城市") {
		t.Errorf("未选城市且未开自动定位应提示选择城市: %v", err)
	}
	if _, err := run(t, p, map[string]any{"city": `{"name":"x","lat":91,"lon":0}`}, nil, nil, nil, ""); err == nil {
		t.Error("纬度越界应报错")
	}
	if _, err := run(t, p, map[string]any{"city": "not json"}, nil, nil, nil, ""); err == nil {
		t.Error("非法城市值应报错")
	}
	// 对象形状同样接受。
	if _, err := run(t, p, map[string]any{"city": map[string]any{"name": "北京", "lat": 39.9, "lon": 116.4}}, nil, nil, nil, ""); err != nil {
		t.Errorf("对象形状应可用: %v", err)
	}
}

func TestLookupReturnsCandidatesByLang(t *testing.T) {
	f := newFake(t)
	f.geoBody.Store(`{"results":[
	  {"id":1816670,"name":"北京","latitude":39.9075,"longitude":116.39723,"timezone":"Asia/Shanghai","country":"中国","admin1":"北京市"},
	  {"id":2,"name":"北京","latitude":29.5,"longitude":106.5,"timezone":"Asia/Shanghai","country":"中国","admin1":"重庆市"},
	  {"id":3,"name":"上海","latitude":31.2,"longitude":121.5,"timezone":"Asia/Shanghai","country":"中国","admin1":"上海"}]}`)
	p := f.plugin()
	cands, err := p.Lookup(context.Background(), "city", " 北京 ", "zh")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("应 3 个候选: %+v", cands)
	}
	if cands[0].Label != "北京, 北京市, 中国" {
		t.Errorf("label=%q", cands[0].Label)
	}
	if cands[2].Label != "上海, 中国" {
		t.Errorf("与 admin1 同名时应去重: %q", cands[2].Label)
	}
	var v struct {
		ID       int64   `json:"id"`
		Name     string  `json:"name"`
		Lat      float64 `json:"lat"`
		Lon      float64 `json:"lon"`
		Timezone string  `json:"tz"`
	}
	if err := json.Unmarshal([]byte(cands[0].Value), &v); err != nil || v.Name != "北京" || v.Lat != 39.9075 || v.Timezone != "Asia/Shanghai" {
		t.Errorf("value 应为可解析的 JSON: %q %v", cands[0].Value, err)
	}
	// Value 可直接作为 city 配置使用。
	if _, err := run(t, p, map[string]any{"city": cands[0].Value}, nil, nil, nil, ""); err != nil {
		t.Errorf("候选值应能直接用于采集: %v", err)
	}
	_, _ = p.Lookup(context.Background(), "city", "Beijing", "en")
	q := f.lastQ.Load().(interface{ Get(string) string })
	if q.Get("language") != "en" || q.Get("name") != "Beijing" {
		t.Errorf("en 查询参数: %v", q)
	}
	_, _ = p.Lookup(context.Background(), "city", "北京", "zh-CN")
	q = f.lastQ.Load().(interface{ Get(string) string })
	if q.Get("language") != "zh" {
		t.Errorf("中文应使用 language=zh: %v", q)
	}
}

func TestLookupNoResultsField(t *testing.T) {
	f := newFake(t)
	f.geoBody.Store(`{"generationtime_ms":0.5}`)
	cands, err := f.plugin().Lookup(context.Background(), "city", "zzzzqq", "zh")
	if err != nil || len(cands) != 0 {
		t.Fatalf("无 results 应返回空候选: %v %v", cands, err)
	}
}

func TestLookupEdgeCases(t *testing.T) {
	f := newFake(t)
	p := f.plugin()
	if c, err := p.Lookup(context.Background(), "city", "  ", "zh"); err != nil || len(c) != 0 || f.search.Load() != 0 {
		t.Errorf("空查询不应发请求: %v %v %d", c, err, f.search.Load())
	}
	if _, err := p.Lookup(context.Background(), "other", "x", "zh"); err == nil {
		t.Error("未知字段应报错")
	}
	if _, err := p.Lookup(context.Background(), "city", strings.Repeat("长", 101), "zh"); err == nil {
		t.Error("过长的查询词应报错")
	}
}

func TestAutoLocateSuccessAndCachedInState(t *testing.T) {
	f := newFake(t)
	p := f.plugin()
	clk := clock.NewFake(time.Unix(1_800_000_000, 0))
	rep, err := run(t, p, map[string]any{"auto_locate": true}, nil, clk, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.locate.Load() != 1 || text(t, rep, "location") != "Beijing" {
		t.Fatalf("应定位 1 次并以城市命名: %d %q", f.locate.Load(), text(t, rep, "location"))
	}
	if rep.State == "" {
		t.Fatal("定位结果应写入 State 供下次复用")
	}
	// 下一轮复用 State，不再查询定位服务。
	clk.Advance(10 * time.Minute)
	if _, err := run(t, p, map[string]any{"auto_locate": true}, nil, clk, rep, rep.State); err != nil {
		t.Fatal(err)
	}
	if f.locate.Load() != 1 {
		t.Errorf("24 小时内不应重复定位: %d", f.locate.Load())
	}
	// 超过 24 小时重新定位。
	clk.Advance(25 * time.Hour)
	if _, err := run(t, p, map[string]any{"auto_locate": true}, nil, clk, rep, rep.State); err != nil {
		t.Fatal(err)
	}
	if f.locate.Load() != 2 {
		t.Errorf("过期后应重新定位: %d", f.locate.Load())
	}
}

func TestManualCityWinsOverAutoLocate(t *testing.T) {
	f := newFake(t)
	if _, err := run(t, f.plugin(), map[string]any{"city": beijing, "auto_locate": true}, nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if f.locate.Load() != 0 {
		t.Error("已选城市时不应自动定位")
	}
}

func TestAutoLocateFailureMessages(t *testing.T) {
	cases := map[string]func(f *fakeAPI){
		"success=false": func(f *fakeAPI) { f.ipBody.Store(`{"success": false, "message": "Invalid IP address"}`) },
		"HTTP 429":      func(f *fakeAPI) { f.ipStatus.Store(429) },
		"缺坐标":           func(f *fakeAPI) { f.ipBody.Store(`{"success": true, "city": "X"}`) },
		"非 JSON":        func(f *fakeAPI) { f.ipBody.Store(`<html>`) },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			setup(f)
			_, err := run(t, f.plugin(), map[string]any{"auto_locate": true}, nil, nil, nil, "")
			if err == nil {
				t.Fatal("应报错")
			}
			if !strings.Contains(err.Error(), "自动定位失败") || !strings.Contains(err.Error(), "手动选择城市") {
				t.Errorf("应提示手动选择城市: %v", err)
			}
			if f.forecast.Load() != 0 {
				t.Error("定位失败不应请求预报")
			}
		})
	}
}

// 城市搜索与自动定位一律直连；预报走实例代理。
func TestProxyUsage(t *testing.T) {
	f := newFake(t)
	fake := proxytest.NewHTTP(t)
	pr, err := proxy.Parse("http://" + fake.Addr)
	if err != nil {
		t.Fatal(err)
	}
	p := f.plugin()

	// 自动定位：即使实例配置了代理，也直连（代理零请求），预报则经过代理。
	_, err = run(t, p, map[string]any{"auto_locate": true}, pr, nil, nil, "")
	if f.locate.Load() != 1 {
		t.Fatalf("定位请求应直达定位服务: %d", f.locate.Load())
	}
	seen := fake.Seen()
	if len(seen) != 1 || !strings.Contains(seen[0].Target, "/v1/forecast") {
		t.Fatalf("代理应只看到预报请求: %+v", seen)
	}
	_ = err // 假代理只回 204 空体，采集结果不在此断言
	if f.forecast.Load() != 0 {
		t.Error("预报请求不应绕过代理直达服务")
	}

	// 城市搜索：直连。
	before := len(fake.Seen())
	if _, err := p.Lookup(context.Background(), "city", "北京", "zh"); err != nil {
		t.Fatal(err)
	}
	if f.search.Load() != 1 || len(fake.Seen()) != before {
		t.Errorf("lookup 不应经过代理: search=%d proxySeen=%d", f.search.Load(), len(fake.Seen()))
	}
}

func TestDirectForecastWithoutProxy(t *testing.T) {
	f := newFake(t)
	if _, err := run(t, f.plugin(), map[string]any{"city": beijing}, nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if f.forecast.Load() != 1 {
		t.Errorf("直连预报: %d", f.forecast.Load())
	}
}

// 每次运行都访问预报服务：最短间隔由运行时按 manifest 的 min_interval 在实例层强制，不在插件内节流。
func TestEveryRunFetchesForecast(t *testing.T) {
	f := newFake(t)
	p := f.plugin()
	clk := clock.NewFake(time.Unix(1_800_000_000, 0))
	cfg := map[string]any{"city": beijing}
	first, err := run(t, p, cfg, nil, clk, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Minute)
	if _, err := run(t, p, cfg, nil, clk, first, first.State); err != nil {
		t.Fatal(err)
	}
	if f.forecast.Load() != 2 {
		t.Errorf("每次运行都应请求预报: %d", f.forecast.Load())
	}
}

func TestEmptyCurrentIsError(t *testing.T) {
	cases := map[string]string{
		"空对象":            `{}`,
		"current 缺失":     `{"daily":{"weather_code":[0,0],"temperature_2m_max":[1,2],"temperature_2m_min":[0,1]}}`,
		"current 全空":     `{"current":{},"daily":{}}`,
		"current 全 null": `{"current":{"temperature_2m":null,"relative_humidity_2m":null,"wind_speed_10m":null,"weather_code":null},"daily":{}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.fcBody.Store(body)
			rep, err := run(t, f.plugin(), map[string]any{"city": beijing}, nil, nil, nil, "")
			if err == nil || rep != nil {
				t.Fatalf("没有任何当前数据应报错（让运行时保留旧值）: %+v %v", rep, err)
			}
		})
	}
}

func TestManifestIntervalDefaults(t *testing.T) {
	m := mustManifest()
	if m.Interval != 10*time.Minute {
		t.Errorf("默认间隔应为 10 分钟: %v", m.Interval)
	}
	if m.MinInterval != 5*time.Minute {
		t.Errorf("最短间隔应为 5 分钟: %v", m.MinInterval)
	}
	if m.Timeout < 2*requestTimeout+5*time.Second {
		t.Errorf("运行超时应为定位与预报两次请求超时之和再留余量: timeout=%v request=%v", m.Timeout, requestTimeout)
	}
	if m.Timeout <= 0 || m.Timeout > 60*time.Second {
		t.Errorf("timeout=%v", m.Timeout)
	}
}
