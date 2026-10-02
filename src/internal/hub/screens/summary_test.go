package screens

import (
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func screensOf(ids ...string) model.Layout {
	l := model.Layout{Grid: model.Grid{Cols: 6, Rows: 4}}
	for _, id := range ids {
		l.Screens = append(l.Screens, model.LayoutScreen{ID: id, Name: id, InRotation: true, Widgets: []model.LayoutWidget{}})
	}
	return l
}

func TestDiff仅重排screen记为reordered(t *testing.T) {
	prev := screensOf("index", "a", "b")
	cur := screensOf("index", "b", "a")
	sum := diff(&prev, cur)
	if !sum.Reordered {
		t.Fatalf("顺序变化应记为 reordered: %+v", sum)
	}
	if sum.WidgetsAdded+sum.WidgetsRemoved+sum.WidgetsChanged != 0 || sum.GridChanged || len(sum.ChangedScreens) != 0 {
		t.Fatalf("仅重排不应带来其他改动: %+v", sum)
	}
}

func TestDiff顺序不变或仅增删screen不算重排(t *testing.T) {
	prev := screensOf("index", "a", "b")
	if sum := diff(&prev, screensOf("index", "a", "b")); sum.Reordered {
		t.Fatalf("顺序未变不应 reordered: %+v", sum)
	}
	// 删除中间一个、新增末尾一个：共同 screen 的相对顺序没变
	if sum := diff(&prev, screensOf("index", "b", "c")); sum.Reordered {
		t.Fatalf("增删不应 reordered: %+v", sum)
	}
	if sum := diff(nil, screensOf("index", "a")); sum.Reordered {
		t.Fatalf("首个版本不应 reordered: %+v", sum)
	}
}
