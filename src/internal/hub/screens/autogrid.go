package screens

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// AutoGridNote 是自动选择网格生成的版本摘要。
const AutoGridNote = "按显示器自动选择网格"

// SeedFunc 按网格返回对应的种子布局；该网格没有种子时 ok 为 false。
type SeedFunc func(grid model.Grid) (layout model.Layout, ok bool)

// UseSeedLayouts 注入种子布局来源（由种子逻辑在装配时提供）；须在对外服务前调用。
func (s *Service) UseSeedLayouts(f SeedFunc) { s.seeds = f }

// RecommendGrid 按已采信的 viewport（CSS 像素，已计入界面缩放）推算默认网格（设计 5.5）：
// 长边不足 900 取 6×4（800×480），不足 1152 取 8×5（1024×600），其余取 10×6（1280×720、1280×800，
// 更高分辨率封顶在 10×6，由界面缩放把 CSS 像素降回表内）。
func RecommendGrid(v model.Viewport) model.Grid {
	switch long := max(v.W, v.H); {
	case long < 900:
		return model.Grid{Cols: 6, Rows: 4}
	case long < 1152:
		return model.Grid{Cols: 8, Rows: 5}
	default:
		return model.Grid{Cols: 10, Rows: 6}
	}
}

// AutoSelectGrid 在显示器 viewport 首次被采信时调用：布局仍是种子原版（只有第 1 版且来源为 seed）
// 且推荐网格与当前网格不同时，换成推荐网格的种子布局并生成新版本（来源 auto）；
// 其余情况（已被编辑、没有该网格的种子、网格相同）不改动。返回是否生成了新版本。
// 之后布局不再自动改动，只能按 RecommendGrid 给出推荐。
func (s *Service) AutoSelectGrid(ctx context.Context, v model.Viewport) (bool, error) {
	if s.seeds == nil {
		return false, nil
	}
	st, err := s.Current(ctx)
	if err != nil {
		return false, err
	}
	if st.Version != 1 || st.Source != model.LayoutSourceSeed {
		return false, nil
	}
	grid := RecommendGrid(v)
	if grid == st.Layout.Grid {
		return false, nil
	}
	layout, ok := s.seeds(grid)
	if !ok {
		return false, nil
	}
	if _, err := s.Save(ctx, st.Version, layout, SaveOptions{Source: model.LayoutSourceAuto, Note: AutoGridNote}); err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			return false, nil // 期间被人编辑了，尊重编辑
		}
		var invalid *InvalidError
		if errors.As(err, &invalid) {
			slog.Warn("种子布局未通过校验，未自动切换网格", "grid", fmt.Sprintf("%dx%d", grid.Cols, grid.Rows), "problems", len(invalid.Problems))
		}
		return false, err
	}
	return true, nil
}
