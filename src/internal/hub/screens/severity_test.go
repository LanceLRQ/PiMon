package screens

import (
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// 休眠与维护同级：比正常与未知更严重，但不压过离线。
func TestRefsState休眠的严重度(t *testing.T) {
	sn := &snapshot{instances: map[string]model.Instance{
		"ok":      {DisplayState: string(report.DisplayOK)},
		"unknown": {DisplayState: string(report.DisplayUnknown)},
		"dormant": {DisplayState: string(report.DisplayDormant)},
		"offline": {DisplayState: string(report.DisplayOffline)},
	}}
	refs := func(ids ...string) []model.WidgetRef {
		var out []model.WidgetRef
		for _, id := range ids {
			out = append(out, model.WidgetRef{InstanceID: id})
		}
		return out
	}
	if got := refsState(refs("ok", "unknown", "dormant"), sn, false); got != "dormant" {
		t.Fatalf("got %s", got)
	}
	if got := refsState(refs("dormant", "offline"), sn, false); got != "offline" {
		t.Fatalf("got %s", got)
	}
}
