package settings

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LanceLRQ/PiMon/src/internal/hub/store"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "pimon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func noEnv(string) string                   { return "" }
func noLink(string) (string, error)         { return "", os.ErrNotExist }
func fixedEnv(v string) func(string) string { return func(string) string { return v } }

func load(t *testing.T, db *store.DB) *Service {
	t.Helper()
	s, err := Load(context.Background(), db, WithGetenv(fixedEnv("Asia/Shanghai")), WithReadlink(noLink))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDefaults_值与通过校验(t *testing.T) {
	d := Defaults("Asia/Shanghai")
	want := model.Settings{
		Language: "zh", Timezone: "Asia/Shanghai", AccessURL: "", TrustedProxies: []string{},
		Retention: model.RetentionSettings{RawHours: 24, FiveMinDays: 30, HourDays: 365},
		Backup:    model.BackupSettings{DailyAt: "04:00", Keep: 7},
	}
	if d.Language != want.Language || d.Timezone != want.Timezone || d.AccessURL != "" ||
		d.TrustedProxies == nil || len(d.TrustedProxies) != 0 || d.HTTPSEnabled || d.ReduceEffects ||
		d.Retention != want.Retention || d.Backup != want.Backup {
		t.Fatalf("默认值不符: %+v", d)
	}
	if err := Validate(d); err != nil {
		t.Fatalf("默认值应通过校验: %v", err)
	}
}

func TestValidate_非法用例(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *model.Settings)
		key    string
		code   string
	}{
		{"语言", func(s *model.Settings) { s.Language = "fr" }, "language", "invalid"},
		{"时区空", func(s *model.Settings) { s.Timezone = "" }, "timezone", "invalid"},
		{"时区Local", func(s *model.Settings) { s.Timezone = "Local" }, "timezone", "invalid"},
		{"时区不存在", func(s *model.Settings) { s.Timezone = "Mars/Base" }, "timezone", "invalid"},
		{"访问地址无协议", func(s *model.Settings) { s.AccessURL = "pimon.local" }, "access_url", "invalid"},
		{"访问地址ftp", func(s *model.Settings) { s.AccessURL = "ftp://pimon.local" }, "access_url", "invalid"},
		{"访问地址无主机", func(s *model.Settings) { s.AccessURL = "http://" }, "access_url", "invalid"},
		{"反代非IP", func(s *model.Settings) { s.TrustedProxies = []string{"10.0.0.1", "nginx"} }, "trusted_proxies[1]", "invalid"},
		{"反代坏CIDR", func(s *model.Settings) { s.TrustedProxies = []string{"10.0.0.0/33"} }, "trusted_proxies[0]", "invalid"},
		{"原始采样为0", func(s *model.Settings) { s.Retention.RawHours = 0 }, "retention.raw_hours", "out_of_range"},
		{"原始采样过大", func(s *model.Settings) { s.Retention.RawHours = 169 }, "retention.raw_hours", "out_of_range"},
		{"5分钟为0", func(s *model.Settings) { s.Retention.FiveMinDays = 0 }, "retention.five_min_days", "out_of_range"},
		{"5分钟过大", func(s *model.Settings) { s.Retention.FiveMinDays = 366 }, "retention.five_min_days", "out_of_range"},
		{"1小时为0", func(s *model.Settings) { s.Retention.HourDays = 0 }, "retention.hour_days", "out_of_range"},
		{"1小时过大", func(s *model.Settings) { s.Retention.HourDays = 1826 }, "retention.hour_days", "out_of_range"},
		{"备份时刻格式", func(s *model.Settings) { s.Backup.DailyAt = "4:00" }, "backup.daily_at", "invalid"},
		{"备份时刻越界", func(s *model.Settings) { s.Backup.DailyAt = "25:00" }, "backup.daily_at", "invalid"},
		{"备份份数为0", func(s *model.Settings) { s.Backup.Keep = 0 }, "backup.keep", "out_of_range"},
		{"备份份数过大", func(s *model.Settings) { s.Backup.Keep = 31 }, "backup.keep", "out_of_range"},
		{"轮播模式", func(s *model.Settings) { s.Screen.CarouselMode = "loop" }, "screen.carousel_mode", "invalid"},
		{"回首页秒数过小", func(s *model.Settings) { s.Screen.IdleHomeSeconds = 9 }, "screen.idle_home_seconds", "out_of_range"},
		{"回首页秒数过大", func(s *model.Settings) { s.Screen.IdleHomeSeconds = 3601 }, "screen.idle_home_seconds", "out_of_range"},
		{"默认停留过小", func(s *model.Settings) { s.Screen.DefaultDwellSeconds = 2 }, "screen.default_dwell_seconds", "out_of_range"},
		{"默认停留过大", func(s *model.Settings) { s.Screen.DefaultDwellSeconds = 3601 }, "screen.default_dwell_seconds", "out_of_range"},
		{"输入方式", func(s *model.Settings) { s.Screen.InputMode = "mouse" }, "screen.input_mode", "invalid"},
		{"界面缩放", func(s *model.Settings) { s.Screen.UIScale = 1.1 }, "screen.ui_scale", "invalid"},
		{"界面缩放为0", func(s *model.Settings) { s.Screen.UIScale = 0 }, "screen.ui_scale", "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Defaults("UTC")
			c.mutate(&s)
			err := Validate(s)
			var fe model.FieldErrors
			if !errors.As(err, &fe) {
				t.Fatalf("应返回 FieldErrors, got %v", err)
			}
			if fe[c.key] != c.code {
				t.Fatalf("want %s=%s, got %v", c.key, c.code, fe)
			}
			if len(fe) != 1 {
				t.Fatalf("只应有一个错误: %v", fe)
			}
		})
	}
}

func TestDefaults_屏幕参数(t *testing.T) {
	want := model.ScreenDisplaySettings{
		CarouselMode: "auto", IdleHomeSeconds: 60, DefaultDwellSeconds: 15, InputMode: "auto", UIScale: 1,
	}
	if got := Defaults("UTC").Screen; got != want {
		t.Fatalf("屏幕参数默认值 = %+v，期望 %+v", got, want)
	}
	for _, scale := range []float64{1, 1.25, 1.5, 2} {
		s := Defaults("UTC")
		s.Screen.UIScale = scale
		if err := Validate(s); err != nil {
			t.Fatalf("缩放 %v 应合法: %v", scale, err)
		}
	}
}

func TestValidate_合法边界(t *testing.T) {
	s := Defaults("UTC")
	s.AccessURL = "https://pi.example.com:8443/"
	s.TrustedProxies = []string{"127.0.0.1", "10.0.0.0/8", "::1", "fd00::/8"}
	s.Retention = model.RetentionSettings{RawHours: 168, FiveMinDays: 365, HourDays: 1825}
	s.Backup = model.BackupSettings{DailyAt: "23:59", Keep: 30}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
	s.Retention = model.RetentionSettings{RawHours: 1, FiveMinDays: 1, HourDays: 1}
	s.Backup = model.BackupSettings{DailyAt: "00:00", Keep: 1}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
}

func TestFieldErrors_实现error且输出稳定(t *testing.T) {
	var err error = model.FieldErrors{"b": "invalid", "a": "out_of_range"}
	if !strings.Contains(err.Error(), "a: out_of_range, b: invalid") {
		t.Fatal(err.Error())
	}
}

func TestLoad_空库用默认值(t *testing.T) {
	s := load(t, openDB(t))
	got := s.Get()
	if got.Language != "zh" || got.Timezone != "Asia/Shanghai" || got.Backup.Keep != 7 {
		t.Fatalf("%+v", got)
	}
	if len(s.TrustedNets()) != 0 {
		t.Fatal("默认不应有受信任网段")
	}
}

func TestUpdate_立即生效且可重新Load读回(t *testing.T) {
	db := openDB(t)
	s := load(t, db)
	n := s.Get()
	n.Language = "en"
	n.HTTPSEnabled = true
	n.TrustedProxies = []string{"10.0.0.0/8", "192.168.1.5"}
	n.Retention.RawHours = 48
	if err := s.Update(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.Language != "en" || !got.HTTPSEnabled || got.Retention.RawHours != 48 {
		t.Fatalf("%+v", got)
	}
	nets := s.TrustedNets()
	if len(nets) != 2 || nets[0] != netip.MustParsePrefix("10.0.0.0/8") || nets[1] != netip.MustParsePrefix("192.168.1.5/32") {
		t.Fatalf("%v", nets)
	}
	again := load(t, db)
	if g := again.Get(); g.Language != "en" || len(g.TrustedProxies) != 2 || again.TrustedNets()[1] != nets[1] {
		t.Fatalf("重新 Load 读回不一致: %+v", g)
	}
}

func TestUpdate_校验失败不落库不改缓存(t *testing.T) {
	db := openDB(t)
	s := load(t, db)
	bad := s.Get()
	bad.Language = "fr"
	err := s.Update(context.Background(), bad)
	var fe model.FieldErrors
	if !errors.As(err, &fe) || fe["language"] != "invalid" {
		t.Fatalf("%v", err)
	}
	if s.Get().Language != "zh" {
		t.Fatal("缓存不应改变")
	}
	if load(t, db).Get().Language != "zh" {
		t.Fatal("库不应改变")
	}
}

func TestGet_返回副本(t *testing.T) {
	s := load(t, openDB(t))
	n := s.Get()
	n.TrustedProxies = []string{"10.0.0.0/8"}
	if err := s.Update(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	g := s.Get()
	g.TrustedProxies[0] = "evil"
	if s.Get().TrustedProxies[0] != "10.0.0.0/8" {
		t.Fatal("Get 应返回副本")
	}
	nets := s.TrustedNets()
	nets[0] = netip.Prefix{}
	if s.TrustedNets()[0] != netip.MustParsePrefix("10.0.0.0/8") {
		t.Fatal("TrustedNets 应返回副本")
	}
}

func TestUpdate_nil反代序列化为空数组(t *testing.T) {
	db := openDB(t)
	s := load(t, db)
	n := s.Get()
	n.TrustedProxies = nil
	if err := s.Update(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if s.Get().TrustedProxies == nil {
		t.Fatal("缓存中应为空切片")
	}
	var raw string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='global'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"trusted_proxies":[]`) {
		t.Fatalf("应序列化为 []: %s", raw)
	}
}

func TestLoad_旧JSON缺字段补默认值(t *testing.T) {
	db := openDB(t)
	old := `{"language":"en","retention":{"raw_hours":48},"trusted_proxies":null}`
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('global',?)`, old); err != nil {
		t.Fatal(err)
	}
	g := load(t, db).Get()
	if g.Language != "en" || g.Retention.RawHours != 48 {
		t.Fatalf("%+v", g)
	}
	if g.Retention.FiveMinDays != 30 || g.Retention.HourDays != 365 || g.Backup.DailyAt != "04:00" ||
		g.Backup.Keep != 7 || g.Timezone != "Asia/Shanghai" {
		t.Fatalf("缺失字段应取默认值: %+v", g)
	}
	if g.TrustedProxies == nil {
		t.Fatal("null 应规整为空切片")
	}
	if g.Screen != Defaults("Asia/Shanghai").Screen {
		t.Fatalf("旧 JSON 没有 screen 时应取默认值: %+v", g.Screen)
	}
}

func TestLoad_库内损坏JSON报错(t *testing.T) {
	db := openDB(t)
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('global','{oops')`); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), db, WithGetenv(noEnv), WithReadlink(noLink)); err == nil {
		t.Fatal("应报错")
	}
}

func TestDetectTimezone(t *testing.T) {
	link := func(target string) func(string) (string, error) {
		return func(p string) (string, error) {
			if p != "/etc/localtime" {
				t.Errorf("意外路径 %s", p)
			}
			return target, nil
		}
	}
	cases := []struct {
		name string
		env  string
		link func(string) (string, error)
		want string
	}{
		{"TZ优先", "Europe/Paris", link("/usr/share/zoneinfo/Asia/Tokyo"), "Europe/Paris"},
		{"TZ带冒号前缀", ":Europe/Paris", noLink, "Europe/Paris"},
		{"TZ非法则走链接", "Nope/Zone", link("/usr/share/zoneinfo/Asia/Tokyo"), "Asia/Tokyo"},
		{"链接linux风格", "", link("/usr/share/zoneinfo/Asia/Tokyo"), "Asia/Tokyo"},
		{"链接mac风格", "", link("/var/db/timezone/zoneinfo/America/New_York"), "America/New_York"},
		{"链接相对路径", "", link("../usr/share/zoneinfo/Asia/Tokyo"), "Asia/Tokyo"},
		{"链接无zoneinfo", "", link("/somewhere/else"), "UTC"},
		{"链接内容非法时区", "", link("/usr/share/zoneinfo/Bad/Zone"), "UTC"},
		{"读链接失败", "", noLink, "UTC"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectTimezone(fixedEnv(c.env), c.link); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestOnChange_仅在更新成功后通知(t *testing.T) {
	s := load(t, openDB(t))
	calls := 0
	s.OnChange(func() { calls++ })
	n := s.Get()
	n.Language = "en"
	if err := s.Update(context.Background(), n); err != nil || calls != 1 {
		t.Fatalf("成功更新应通知一次: calls=%d err=%v", calls, err)
	}
	bad := s.Get()
	bad.Timezone = "Not/AZone"
	if err := s.Update(context.Background(), bad); err == nil || calls != 1 {
		t.Fatalf("校验失败不应通知: calls=%d err=%v", calls, err)
	}
}
