package screens

import (
	"fmt"
	"slices"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// Placement 是参与几何校验的一个小组件：位置、尺寸与允许的尺寸集合。
type Placement struct {
	ID  string
	Col int
	Row int
	W   int
	H   int
	// Allowed 是允许的尺寸，形如 "2x1"；nil 表示不限制尺寸，空切片表示任何尺寸都不允许。
	Allowed []string
}

func sizeKey(w, h int) string { return fmt.Sprintf("%dx%d", w, h) }

// CheckGeometry 校验同一个 screen 上的小组件：尺寸合法、在白名单内、不越界、互不重叠。
// 规则与 testdata/grid_cases.json 一致（前端读同一份用例）。返回的问题不带 Screen。
//
// 尺寸不合法（列或行小于 1）的小组件只报 invalid_size，不再参与其余检查；
// 每一对重叠只报一条，Widget 为靠后的那个，With 为靠前的那个。
func CheckGeometry(g model.Grid, ws []Placement) []model.LayoutProblem {
	out := []model.LayoutProblem{}
	valid := make([]bool, len(ws))
	for i, w := range ws {
		if w.W < 1 || w.H < 1 {
			out = append(out, model.LayoutProblem{Widget: w.ID, Code: model.LayoutProblemInvalidSize})
			continue
		}
		valid[i] = true
		if w.Allowed != nil && !slices.Contains(w.Allowed, sizeKey(w.W, w.H)) {
			out = append(out, model.LayoutProblem{Widget: w.ID, Code: model.LayoutProblemSizeNotAllowed})
		}
		if w.Col < 0 || w.Row < 0 || w.Col+w.W > g.Cols || w.Row+w.H > g.Rows {
			out = append(out, model.LayoutProblem{Widget: w.ID, Code: model.LayoutProblemOutOfBounds})
		}
	}
	for j := range ws {
		if !valid[j] {
			continue
		}
		for i := 0; i < j; i++ {
			if valid[i] && intersects(ws[i], ws[j]) {
				out = append(out, model.LayoutProblem{Widget: ws[j].ID, Code: model.LayoutProblemOverlap, With: ws[i].ID})
			}
		}
	}
	return out
}

func intersects(a, b Placement) bool {
	return a.Col < b.Col+b.W && b.Col < a.Col+a.W && a.Row < b.Row+b.H && b.Row < a.Row+a.H
}
