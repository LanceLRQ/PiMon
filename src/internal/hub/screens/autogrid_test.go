package screens

import (
	"context"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

func TestRecommendGrid(t *testing.T) {
	cases := []struct {
		w, h int
		want model.Grid
	}{
		{800, 480, model.Grid{Cols: 6, Rows: 4}},
		{1024, 600, model.Grid{Cols: 8, Rows: 5}},
		{1280, 720, model.Grid{Cols: 10, Rows: 6}},
		{1280, 800, model.Grid{Cols: 10, Rows: 6}},
		{1920, 1080, model.Grid{Cols: 10, Rows: 6}},
		{720, 1280, model.Grid{Cols: 10, Rows: 6}},
	}
	for _, c := range cases {
		if got := RecommendGrid(model.Viewport{W: c.w, H: c.h, DPR: 1}); got != c.want {
			t.Errorf("%dx%d 推荐 %+v，期望 %+v", c.w, c.h, got, c.want)
		}
	}
}

// fakeSeeds 只认 10x6：返回只有首页的空布局。
func fakeSeeds(grid model.Grid) (model.Layout, bool) {
	if grid.Cols != 10 || grid.Rows != 6 {
		return model.Layout{}, false
	}
	return model.Layout{Grid: grid, Screens: []model.LayoutScreen{
		{ID: "index", Name: "首页", InRotation: true, Widgets: []model.LayoutWidget{}},
	}}, true
}

func seedV1(t *testing.T, f *fixture) {
	t.Helper()
	if _, err := f.svc.Save(context.Background(), 0, layoutWith(), SaveOptions{Source: model.LayoutSourceSeed}); err != nil {
		t.Fatal(err)
	}
}

func TestAutoSelectGrid_种子原版时换成推荐网格并生成新版本(t *testing.T) {
	f := newFixture(t)
	f.svc.UseSeedLayouts(fakeSeeds)
	seedV1(t, f)

	applied, err := f.svc.AutoSelectGrid(context.Background(), model.Viewport{W: 1280, H: 720, DPR: 1})
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	st, _ := f.svc.Current(context.Background())
	if st.Version != 2 || st.Source != model.LayoutSourceAuto || st.Layout.Grid != (model.Grid{Cols: 10, Rows: 6}) {
		t.Fatalf("当前布局不对: %+v", st)
	}
	vs, _ := f.svc.Versions(context.Background())
	if vs[0].Summary.Note != AutoGridNote {
		t.Fatalf("摘要 = %q", vs[0].Summary.Note)
	}

	// 之后不再自动改：换一个 viewport 也不动。
	if applied, err = f.svc.AutoSelectGrid(context.Background(), model.Viewport{W: 1280, H: 720, DPR: 1}); err != nil || applied {
		t.Fatalf("已自动换过不应再改: applied=%v err=%v", applied, err)
	}
}

func TestAutoSelectGrid_不改动的情形(t *testing.T) {
	ctx := context.Background()
	vp := model.Viewport{W: 1280, H: 720, DPR: 1}

	t.Run("尚无版本", func(t *testing.T) {
		f := newFixture(t)
		f.svc.UseSeedLayouts(fakeSeeds)
		if applied, err := f.svc.AutoSelectGrid(ctx, vp); err != nil || applied {
			t.Fatalf("applied=%v err=%v", applied, err)
		}
	})
	t.Run("已被编辑", func(t *testing.T) {
		f := newFixture(t)
		f.svc.UseSeedLayouts(fakeSeeds)
		seedV1(t, f)
		mustSave(t, f, 1, layoutWith())
		if applied, err := f.svc.AutoSelectGrid(ctx, vp); err != nil || applied {
			t.Fatalf("applied=%v err=%v", applied, err)
		}
		if st, _ := f.svc.Current(ctx); st.Version != 2 || st.Source != model.LayoutSourceEdit {
			t.Fatalf("不应生成新版本: %+v", st)
		}
	})
	t.Run("网格相同", func(t *testing.T) {
		f := newFixture(t)
		f.svc.UseSeedLayouts(fakeSeeds)
		seedV1(t, f) // 6x4
		if applied, err := f.svc.AutoSelectGrid(ctx, model.Viewport{W: 800, H: 480, DPR: 1}); err != nil || applied {
			t.Fatalf("applied=%v err=%v", applied, err)
		}
	})
	t.Run("该网格没有种子", func(t *testing.T) {
		f := newFixture(t)
		f.svc.UseSeedLayouts(fakeSeeds)
		seedV1(t, f)
		if applied, err := f.svc.AutoSelectGrid(ctx, model.Viewport{W: 1024, H: 600, DPR: 1}); err != nil || applied {
			t.Fatalf("applied=%v err=%v", applied, err)
		}
	})
	t.Run("未注入种子", func(t *testing.T) {
		f := newFixture(t)
		seedV1(t, f)
		if applied, err := f.svc.AutoSelectGrid(ctx, vp); err != nil || applied {
			t.Fatalf("applied=%v err=%v", applied, err)
		}
	})
}
