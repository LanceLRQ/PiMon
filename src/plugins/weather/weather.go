// Package weather 是内置天气插件：用 Open-Meteo 取当前天气与明日最高/最低温。
//
// 位置来源（优先级）：已选择的城市（lookup 字段，城市搜索走 Open-Meteo 地理编码）；
// 否则在开启 auto_locate 时按出口 IP 调 ipwho.is 定位。城市搜索与自动定位一律直连，
// 只有预报请求走实例配置的代理。自动定位结果写进报告 State，24 小时内复用。
//
// 数据项（键：类型）：location、condition、icon、tomorrow_condition、tomorrow_icon、attribution 为 text；
// temperature（°C）、wind_speed（km/h）、wind_direction（°）、tomorrow_high、tomorrow_low 为 number；
// humidity 为 gauge（%）。上游缺测（null）的字段不输出，不当作零。
//
// 天气文案为「中文 / English」双语，图标 key 见 wmo 表（夜间的晴与多云加 -night 后缀），
// 前端按 key 选图标；署名文本放在 attribution，供小组件详情层与「关于」页展示。
//
// 最短刷新间隔 5 分钟由 manifest 的 min_interval 声明，实例层校验 interval_seconds；
// 插件本身每次运行都会请求预报。当前数据全部缺失时返回错误，让运行时保留旧值并标记过期。
package weather

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/pkg/clock"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/proxy"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/runtime"
	"github.com/LanceLRQ/PiMon/src/plugins/internal/cfg"
)

//go:embed plugin.yaml
var manifestYAML []byte

const (
	defaultForecastBase = "https://api.open-meteo.com"
	defaultGeocodeBase  = "https://geocoding-api.open-meteo.com"
	defaultIPBase       = "https://ipwho.is/"

	// attribution 是 Open-Meteo 要求的署名（CC BY 4.0）；数据仅做单位展示，未作修改。
	attribution = "Weather data by Open-Meteo.com (https://open-meteo.com/), CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)"

	// maxBody 是单个外部响应体的大小上限。
	maxBody = 1 << 20
	// requestTimeout 是单个外部请求的超时。
	requestTimeout = 10 * time.Second
	// locateTTL 是自动定位结果的缓存时长。
	locateTTL = 24 * time.Hour

	maxQueryRunes = 100
	maxCandidates = 8
)

// wmoEntry 是一个 WMO 天气代码的中英文文案与图标 key。
type wmoEntry struct{ zh, en, icon string }

// wmo 是 Open-Meteo 文档列出的全部天气代码（见 docs/_internal/research/07）。
var wmo = map[int]wmoEntry{
	0: {"晴", "Clear sky", "clear"}, 1: {"大部晴朗", "Mainly clear", "clear"},
	2: {"局部多云", "Partly cloudy", "partly-cloudy"}, 3: {"阴", "Overcast", "cloudy"},
	45: {"雾", "Fog", "fog"}, 48: {"冻雾", "Depositing rime fog", "fog"},
	51: {"小毛毛雨", "Light drizzle", "drizzle"}, 53: {"中等毛毛雨", "Moderate drizzle", "drizzle"},
	55: {"大毛毛雨", "Dense drizzle", "drizzle"}, 56: {"小冻毛毛雨", "Light freezing drizzle", "drizzle"},
	57: {"大冻毛毛雨", "Dense freezing drizzle", "drizzle"},
	61: {"小雨", "Slight rain", "rain"}, 63: {"中雨", "Moderate rain", "rain"}, 65: {"大雨", "Heavy rain", "rain"},
	66: {"小冻雨", "Light freezing rain", "rain"}, 67: {"大冻雨", "Heavy freezing rain", "rain"},
	71: {"小雪", "Slight snowfall", "snow"}, 73: {"中雪", "Moderate snowfall", "snow"},
	75: {"大雪", "Heavy snowfall", "snow"}, 77: {"雪粒", "Snow grains", "snow"},
	80: {"小阵雨", "Slight rain showers", "showers"}, 81: {"中等阵雨", "Moderate rain showers", "showers"},
	82: {"强阵雨", "Violent rain showers", "showers"},
	85: {"小阵雪", "Slight snow showers", "snow"}, 86: {"大阵雪", "Heavy snow showers", "snow"},
	95: {"雷暴", "Thunderstorm", "thunderstorm"}, 96: {"雷暴伴小冰雹", "Thunderstorm with slight hail", "thunderstorm"},
	99: {"雷暴伴大冰雹", "Thunderstorm with heavy hail", "thunderstorm"},
}

// describe 返回天气文案与图标 key；night 为真时晴与多云用夜间图标。未知代码回落 unknown，不当作晴天。
func describe(code int, night bool) (text, icon string) {
	e, ok := wmo[code]
	if !ok {
		return "未知 / Unknown", "unknown"
	}
	icon = e.icon
	if night && (icon == "clear" || icon == "partly-cloudy") {
		icon += "-night"
	}
	return e.zh + " / " + e.en, icon
}

type plugin struct {
	m            *manifest.Manifest
	forecastBase string
	geocodeBase  string
	ipBase       string
}

func mustManifest() *manifest.Manifest {
	m, err := manifest.Parse(manifestYAML)
	if err != nil {
		panic("weather 内置 manifest 不合法: " + err.Error())
	}
	return m
}

func init() {
	runtime.Register(&plugin{m: mustManifest(), forecastBase: defaultForecastBase, geocodeBase: defaultGeocodeBase, ipBase: defaultIPBase})
}

func (p *plugin) Manifest() *manifest.Manifest { return p.m }

// location 是城市配置值的形状，同时是 lookup 候选 Value 的 JSON 形状。
type location struct {
	ID       int64   `json:"id,omitempty"`
	Name     string  `json:"name"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Timezone string  `json:"tz,omitempty"`
}

func (l location) valid() bool {
	return l.Name != "" && l.Lat >= -90 && l.Lat <= 90 && l.Lon >= -180 && l.Lon <= 180
}

// state 是写入报告 State 的私有状态。
type state struct {
	// Located 是自动定位的缓存结果与时间（Unix 毫秒）。
	Located   *location `json:"located,omitempty"`
	LocatedAt int64     `json:"located_at,omitempty"`
}

func parseState(s string) state {
	var st state
	if s != "" {
		_ = json.Unmarshal([]byte(s), &st)
	}
	return st
}

// errLocateHint 包装自动定位失败，统一提示手动选择城市。
func errLocateHint(reason string) error {
	return fmt.Errorf("自动定位失败（%s），请手动选择城市 / Auto-locate failed (%s); please choose a city manually", reason, reason)
}

// getJSON 发起 GET 并把响应体（限 maxBody）解码进 out。返回的错误不含完整 URL（查询串含坐标）。
func getJSON(ctx context.Context, tr *http.Transport, rawURL string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return errors.New("请求地址不合法")
	}
	host := req.URL.Host
	cli := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	resp, err := cli.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("请求 %s 超时或被取消: %w", host, ctx.Err())
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("请求 %s 失败: %w", host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回 HTTP %d", host, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("读取 %s 响应失败: %w", host, err)
	}
	if len(body) > maxBody {
		return fmt.Errorf("%s 响应过大", host)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s 响应不是合法 JSON", host)
	}
	return nil
}

// directTransport 返回直连的传输层（显式不读环境代理）。
func directTransport() *http.Transport { return proxy.Direct().Transport() }

// Lookup 实现城市搜索：直连 Open-Meteo 地理编码。
func (p *plugin) Lookup(ctx context.Context, key, query, lang string) ([]runtime.Candidate, error) {
	if key != "city" {
		return nil, fmt.Errorf("未知的 lookup 字段 %q", key)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(query) > maxQueryRunes {
		return nil, fmt.Errorf("查询词过长（最多 %d 个字符）", maxQueryRunes)
	}
	// 地理编码的中文语言码是 zh，zh-CN 无效。
	language := "en"
	if strings.HasPrefix(lang, "zh") {
		language = "zh"
	}
	q := url.Values{"name": {query}, "count": {strconv.Itoa(maxCandidates)}, "language": {language}, "format": {"json"}}
	var resp struct {
		Results []struct {
			ID        int64    `json:"id"`
			Name      string   `json:"name"`
			Latitude  *float64 `json:"latitude"`
			Longitude *float64 `json:"longitude"`
			Timezone  string   `json:"timezone"`
			Country   string   `json:"country"`
			Admin1    string   `json:"admin1"`
		} `json:"results"`
	}
	if err := getJSON(ctx, directTransport(), p.geocodeBase+"/v1/search?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	// 无匹配时响应没有 results 字段，按空候选处理。
	out := make([]runtime.Candidate, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Latitude == nil || r.Longitude == nil {
			continue
		}
		loc := location{ID: r.ID, Name: r.Name, Lat: *r.Latitude, Lon: *r.Longitude, Timezone: r.Timezone}
		if !loc.valid() {
			continue
		}
		val, _ := json.Marshal(loc)
		out = append(out, runtime.Candidate{Value: string(val), Label: joinLabel(r.Name, r.Admin1, r.Country)})
	}
	return out, nil
}

// joinLabel 用逗号连接非空部分，并去掉与前一项相同的部分（如「上海, 上海」）。
func joinLabel(parts ...string) string {
	var out []string
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s == "" || (len(out) > 0 && out[len(out)-1] == s) {
			continue
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

// parseCity 读取 city 配置：JSON 字符串或对象。未配置返回 ok=false。
func parseCity(raw any) (loc location, ok bool, err error) {
	var b []byte
	switch v := raw.(type) {
	case nil:
		return location{}, false, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return location{}, false, nil
		}
		b = []byte(v)
	case map[string]any:
		b, _ = json.Marshal(v)
	default:
		return location{}, false, errors.New("城市配置格式不正确")
	}
	if err := json.Unmarshal(b, &loc); err != nil {
		return location{}, false, errors.New("城市配置格式不正确，请重新选择城市")
	}
	if !loc.valid() {
		return location{}, false, errors.New("城市配置不完整或坐标越界，请重新选择城市")
	}
	return loc, true, nil
}

// autoLocate 按出口 IP 定位，强制直连。
func (p *plugin) autoLocate(ctx context.Context) (location, error) {
	var resp struct {
		Success   bool     `json:"success"`
		City      string   `json:"city"`
		Region    string   `json:"region"`
		Country   string   `json:"country"`
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
		Timezone  struct {
			ID string `json:"id"`
		} `json:"timezone"`
	}
	endpoint := p.ipBase + "?fields=success,city,region,country,latitude,longitude,timezone"
	if err := getJSON(ctx, directTransport(), endpoint, &resp); err != nil {
		return location{}, errLocateHint(err.Error())
	}
	if !resp.Success {
		return location{}, errLocateHint("定位服务未能识别本机 IP")
	}
	if resp.Latitude == nil || resp.Longitude == nil {
		return location{}, errLocateHint("定位服务未返回坐标")
	}
	name := joinLabel(resp.City, resp.Region, resp.Country)
	if name == "" {
		name = "auto"
	}
	loc := location{Name: name, Lat: *resp.Latitude, Lon: *resp.Longitude, Timezone: resp.Timezone.ID}
	if !loc.valid() {
		return location{}, errLocateHint("定位服务返回的坐标不合法")
	}
	// 展示名用城市（缺省回落到更大的区域名），避免把整串行政区塞进小组件。
	if resp.City != "" {
		loc.Name = resp.City
	}
	return loc, nil
}

// errNoCity 表示既没有选城市也没有开启自动定位；这不是采集失败，而是尚未配置完成。
var errNoCity = errors.New("未选择城市 / No city chosen")

// cityRequiredKey 是未选城市提示的 i18n 键（前端按 plugin.weather.<文本> 查找）。
const cityRequiredKey = "weather.city_required"

// setupReport 是未选城市时的报告：状态未知，并给出 setup 项供模板显示「请选择城市」占位。
func setupReport(now time.Time) *report.Report {
	return &report.Report{
		Status: report.StatusUnknown, CollectedAt: now.UnixMilli(),
		Items: []report.Item{{Key: "setup", Type: report.TypeState, State: report.StatusUnknown, Text: cityRequiredKey}},
	}
}

// resolve 确定本次使用的位置，并返回更新后的 state。
func (p *plugin) resolve(ctx context.Context, in runtime.Input, now time.Time, st state) (location, state, error) {
	loc, ok, err := parseCity(in.Config["city"])
	if err != nil {
		return location{}, st, err
	}
	if ok {
		return loc, st, nil
	}
	if !cfg.Bool(in.Config, "auto_locate", false) {
		return location{}, st, errNoCity
	}
	if st.Located != nil && st.Located.valid() && now.Sub(time.UnixMilli(st.LocatedAt)) < locateTTL {
		return *st.Located, st, nil
	}
	loc, err = p.autoLocate(ctx)
	if err != nil {
		return location{}, st, err
	}
	st.Located, st.LocatedAt = &loc, now.UnixMilli()
	return loc, st, nil
}

// forecastResp 是预报响应中用到的部分；缺测字段为 nil。
type forecastResp struct {
	Current struct {
		Temperature   *float64 `json:"temperature_2m"`
		Humidity      *float64 `json:"relative_humidity_2m"`
		WindSpeed     *float64 `json:"wind_speed_10m"`
		WindDirection *float64 `json:"wind_direction_10m"`
		WeatherCode   *float64 `json:"weather_code"`
		IsDay         *float64 `json:"is_day"`
	} `json:"current"`
	Daily struct {
		WeatherCode []*float64 `json:"weather_code"`
		Max         []*float64 `json:"temperature_2m_max"`
		Min         []*float64 `json:"temperature_2m_min"`
	} `json:"daily"`
}

// hasCurrent 报告当前天气是否至少有一项可用数据。
func (f *forecastResp) hasCurrent() bool {
	c := f.Current
	return c.Temperature != nil || c.Humidity != nil || c.WindSpeed != nil || c.WeatherCode != nil
}

func (p *plugin) fetchForecast(ctx context.Context, loc location, pr *proxy.Proxy) (*forecastResp, error) {
	q := url.Values{
		"latitude":  {strconv.FormatFloat(loc.Lat, 'f', -1, 64)},
		"longitude": {strconv.FormatFloat(loc.Lon, 'f', -1, 64)},
		"current":   {"temperature_2m,relative_humidity_2m,wind_speed_10m,wind_direction_10m,weather_code,is_day"},
		"daily":     {"weather_code,temperature_2m_max,temperature_2m_min"},
		"timezone":  {"auto"},
		// 只需要今天与明天。
		"forecast_days": {"2"},
	}
	var resp forecastResp
	if err := getJSON(ctx, pr.Transport(), p.forecastBase+"/v1/forecast?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (p *plugin) Collect(ctx context.Context, in runtime.Input) (*report.Report, error) {
	clk := in.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	now := clk.Now()
	st := parseState(in.State)
	loc, st, err := p.resolve(ctx, in, now, st)
	if errors.Is(err, errNoCity) {
		return setupReport(now), nil
	}
	if err != nil {
		return nil, err
	}

	fc, err := p.fetchForecast(ctx, loc, in.Proxy)
	if err != nil {
		return nil, err
	}
	if !fc.hasCurrent() {
		return nil, errors.New("预报响应缺少当前天气数据 / Forecast response has no current conditions")
	}
	rep := buildReport(loc, fc, now)
	rep.State = encodeState(st)
	return rep, nil
}

func encodeState(st state) string {
	b, _ := json.Marshal(st)
	return string(b)
}

func buildReport(loc location, fc *forecastResp, now time.Time) *report.Report {
	rep := &report.Report{Status: report.StatusOK, CollectedAt: now.UnixMilli()}
	add := func(it report.Item) { rep.Items = append(rep.Items, it) }
	addNum := func(key string, v *float64, unit string) {
		if v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) {
			add(report.Item{Key: key, Type: report.TypeNumber, Value: v, Unit: unit})
		}
	}
	addText := func(key, s string) { add(report.Item{Key: key, Type: report.TypeText, Text: s}) }

	addText("location", loc.Name)
	c := fc.Current
	addNum("temperature", c.Temperature, "°C")
	if c.Humidity != nil {
		lo, hi := 0.0, 100.0
		add(report.Item{Key: "humidity", Type: report.TypeGauge, Value: c.Humidity, Unit: "%", Min: &lo, Max: &hi})
	}
	addNum("wind_speed", c.WindSpeed, "km/h")
	addNum("wind_direction", c.WindDirection, "°")
	if c.WeatherCode != nil {
		night := c.IsDay != nil && *c.IsDay == 0
		txt, icon := describe(int(*c.WeatherCode), night)
		addText("condition", txt)
		addText("icon", icon)
	}
	// 明日取 daily 的第 2 项（索引 1）；数组不足时不输出。
	if d := fc.Daily; len(d.Max) > 1 || len(d.Min) > 1 || len(d.WeatherCode) > 1 {
		if len(d.Max) > 1 {
			addNum("tomorrow_high", d.Max[1], "°C")
		}
		if len(d.Min) > 1 {
			addNum("tomorrow_low", d.Min[1], "°C")
		}
		if len(d.WeatherCode) > 1 && d.WeatherCode[1] != nil {
			txt, icon := describe(int(*d.WeatherCode[1]), false)
			addText("tomorrow_condition", txt)
			addText("tomorrow_icon", icon)
		}
	}
	addText("attribution", attribution)

	rep.Summary = loc.Name
	if c.Temperature != nil {
		rep.Summary += " " + strconv.FormatFloat(*c.Temperature, 'f', 1, 64) + "°C"
	}
	if it := findText(rep, "condition"); it != "" {
		rep.Summary += " " + it
	}
	return rep
}

func findText(rep *report.Report, key string) string {
	if it := rep.Find(key); it != nil {
		return it.Text
	}
	return ""
}
