package app

import (
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func TestKioskRSSFromStatus(t *testing.T) {
	rss := int64(123456)
	cases := []struct {
		name string
		k    *model.KioskStatus
		want int64
		ok   bool
	}{
		{"从未上报", nil, 0, false},
		{"在线有值", &model.KioskStatus{Online: true, ChromiumRSSBytes: &rss}, rss, true},
		{"离线", &model.KioskStatus{Online: false, ChromiumRSSBytes: &rss}, 0, false},
		{"值为空", &model.KioskStatus{Online: true}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := kioskRSS(model.ScreenStatus{Kiosk: c.k})
			if got != c.want || ok != c.ok {
				t.Errorf("得到 %d,%v，期望 %d,%v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestHubStatsKioskRSSUnwired(t *testing.T) {
	if _, ok := (hubStats{}).KioskChromiumRSS(); ok {
		t.Error("来源未接入应为未知")
	}
}
