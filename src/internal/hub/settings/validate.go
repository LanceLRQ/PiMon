package settings

import (
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"time"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// Defaults 返回默认设置；timezone 为探测到的系统时区。
func Defaults(timezone string) model.Settings {
	return model.Settings{
		Language:       "zh",
		Timezone:       timezone,
		TrustedProxies: []string{},
		Screen: model.ScreenDisplaySettings{
			CarouselMode: model.CarouselAuto, IdleHomeSeconds: 60, DefaultDwellSeconds: 15,
			InputMode: model.InputAuto, UIScale: 1,
		},
		Retention: model.RetentionSettings{RawHours: 24, FiveMinDays: 30, HourDays: 365},
		Backup:    model.BackupSettings{DailyAt: "04:00", Keep: 7},
	}
}

// Validate 校验设置；失败时返回 model.FieldErrors，全部合法返回 nil。
func Validate(s model.Settings) error {
	fe := model.FieldErrors{}
	if s.Language != "zh" && s.Language != "en" {
		fe["language"] = model.FieldInvalid
	}
	if s.Timezone == "" {
		fe["timezone"] = model.FieldInvalid
	} else if !validZone(s.Timezone) {
		fe["timezone"] = model.FieldInvalid
	}
	if s.AccessURL != "" && !validAccessURL(s.AccessURL) {
		fe["access_url"] = model.FieldInvalid
	}
	for i, p := range s.TrustedProxies {
		if _, err := parseProxy(p); err != nil {
			fe[fmt.Sprintf("trusted_proxies[%d]", i)] = model.FieldInvalid
		}
	}
	validateScreen(fe, s.Screen)
	checkRange(fe, "retention.raw_hours", s.Retention.RawHours, 1, 168)
	checkRange(fe, "retention.five_min_days", s.Retention.FiveMinDays, 1, 365)
	checkRange(fe, "retention.hour_days", s.Retention.HourDays, 1, 1825)
	if !validClock(s.Backup.DailyAt) {
		fe["backup.daily_at"] = model.FieldInvalid
	}
	checkRange(fe, "backup.keep", s.Backup.Keep, 1, 30)
	if len(fe) == 0 {
		return nil
	}
	return fe
}

func checkRange(fe model.FieldErrors, key string, v, lo, hi int) {
	if v < lo || v > hi {
		fe[key] = model.FieldOutOfRange
	}
}

func validAccessURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

// validClock 要求严格的 HH:MM（两位小时、两位分钟）。
func validClock(v string) bool {
	if len(v) != 5 {
		return false
	}
	_, err := time.Parse("15:04", v)
	return err == nil
}

// parseProxy 把单个 IP 或 CIDR 解析为网段；单个 IP 视为满掩码网段。
func parseProxy(v string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(v); err == nil {
		return p, nil
	}
	a, err := netip.ParseAddr(v)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// validateScreen 校验屏幕显示参数。
func validateScreen(fe model.FieldErrors, v model.ScreenDisplaySettings) {
	if v.CarouselMode != model.CarouselHomeOnly && v.CarouselMode != model.CarouselAuto {
		fe["screen.carousel_mode"] = model.FieldInvalid
	}
	checkRange(fe, "screen.idle_home_seconds", v.IdleHomeSeconds, 10, 3600)
	checkRange(fe, "screen.default_dwell_seconds", v.DefaultDwellSeconds, 3, 3600)
	if v.InputMode != model.InputAuto && v.InputMode != model.InputTouch && v.InputMode != model.InputNone {
		fe["screen.input_mode"] = model.FieldInvalid
	}
	if !slices.Contains([]float64{1, 1.25, 1.5, 2}, v.UIScale) {
		fe["screen.ui_scale"] = model.FieldInvalid
	}
}
