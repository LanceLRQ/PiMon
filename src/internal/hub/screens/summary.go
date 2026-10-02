package screens

import (
	"encoding/json"
	"slices"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// diff 计算 cur 相对 prev 的版本摘要；prev 为 nil（首个版本）时全部算新增。
// 小组件在 screen 之间移动算删除加新增。
func diff(prev *model.Layout, cur model.Layout) model.LayoutSummary {
	sum := model.LayoutSummary{ChangedScreens: []string{}}
	if prev == nil {
		sum.GridChanged = true
		for _, sc := range cur.Screens {
			sum.ChangedScreens = append(sum.ChangedScreens, sc.ID)
			sum.WidgetsAdded += len(sc.Widgets)
		}
		return sum
	}
	sum.GridChanged = prev.Grid != cur.Grid
	sum.Reordered = reordered(*prev, cur)

	prevByID := map[string]model.LayoutScreen{}
	for _, sc := range prev.Screens {
		prevByID[sc.ID] = sc
	}
	curIDs := map[string]bool{}
	for _, sc := range cur.Screens {
		curIDs[sc.ID] = true
		p, existed := prevByID[sc.ID]
		if !existed {
			sum.ChangedScreens = append(sum.ChangedScreens, sc.ID)
			sum.WidgetsAdded += len(sc.Widgets)
			continue
		}
		added, removed, changed := diffWidgets(p.Widgets, sc.Widgets)
		sum.WidgetsAdded += added
		sum.WidgetsRemoved += removed
		sum.WidgetsChanged += changed
		if added+removed+changed > 0 || !sameJSON(screenMeta(p), screenMeta(sc)) {
			sum.ChangedScreens = append(sum.ChangedScreens, sc.ID)
		}
	}
	for _, sc := range prev.Screens {
		if !curIDs[sc.ID] {
			sum.ChangedScreens = append(sum.ChangedScreens, sc.ID)
			sum.WidgetsRemoved += len(sc.Widgets)
		}
	}
	return sum
}

// reordered 判断前后两版都有的 screen 之间的相对顺序是否变了（只看共同的 screen，增删不算重排）。
func reordered(prev, cur model.Layout) bool {
	inPrev := map[string]bool{}
	for _, sc := range prev.Screens {
		inPrev[sc.ID] = true
	}
	inCur := map[string]bool{}
	for _, sc := range cur.Screens {
		inCur[sc.ID] = true
	}
	var a, b []string
	for _, sc := range prev.Screens {
		if inCur[sc.ID] {
			a = append(a, sc.ID)
		}
	}
	for _, sc := range cur.Screens {
		if inPrev[sc.ID] {
			b = append(b, sc.ID)
		}
	}
	return !slices.Equal(a, b)
}

// screenMeta 取 screen 除小组件外的属性（名称、停留、是否轮播）。
func screenMeta(sc model.LayoutScreen) model.LayoutScreen {
	sc.Widgets = nil
	return sc
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func diffWidgets(prev, cur []model.LayoutWidget) (added, removed, changed int) {
	prevByID := make(map[string]model.LayoutWidget, len(prev))
	for _, w := range prev {
		prevByID[w.ID] = w
	}
	seen := map[string]bool{}
	for _, w := range cur {
		seen[w.ID] = true
		p, ok := prevByID[w.ID]
		switch {
		case !ok:
			added++
		case !sameJSON(p, w):
			changed++
		}
	}
	for _, w := range prev {
		if !seen[w.ID] {
			removed++
		}
	}
	return added, removed, changed
}
