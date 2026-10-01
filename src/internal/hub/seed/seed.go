// Package seed 在首次启动（库里没有任何布局版本）时创建默认实例与默认首页布局，
// 并按网格提供三份种子布局（6×4、8×5、10×6），供按显示器自动选择网格时取用。
package seed

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/LanceLRQ/PiMon/src/internal/hub/screens"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// 种子实例涉及的插件 id。
const (
	pluginCore    = "core"
	pluginWeather = "weather"
	pluginHost    = "host-metrics"
	pluginReach   = "net-reach"
)

// defaultGrid 是首次启动写入的种子布局网格（设计 5.5 的 1024×600 默认屏）。
var defaultGrid = model.Grid{Cols: 8, Rows: 5}

// InstanceStore 是种子逻辑需要的实例服务，由 instances.Service 实现。
type InstanceStore interface {
	List(ctx context.Context) ([]model.Instance, error)
	Create(ctx context.Context, in model.InstanceInput) (model.InstanceDetail, error)
}

// LayoutStore 是种子逻辑需要的布局服务，由 screens.Service 实现。
type LayoutStore interface {
	Current(ctx context.Context) (model.LayoutState, error)
	Save(ctx context.Context, baseVersion int, l model.Layout, opt screens.SaveOptions) (model.LayoutState, error)
}

// Config 装配种子逻辑。
type Config struct {
	Instances InstanceStore
	Layouts   LayoutStore
	// Language 返回当前界面语言（zh 或 en），每次调用时现取。
	Language func() string
}

// Seeder 创建种子数据并提供按网格取用的种子布局。
type Seeder struct{ cfg Config }

// New 创建 Seeder。
func New(cfg Config) *Seeder { return &Seeder{cfg: cfg} }

// instanceSpec 是一个种子实例：同插件已存在实例时复用，不重复创建。
type instanceSpec struct {
	plugin string
	zh, en string
}

var instanceSpecs = []instanceSpec{
	{pluginCore, "核心", "Core"},
	{pluginWeather, "天气", "Weather"},
	{pluginHost, "本机主机指标", "Hub host metrics"},
	{pluginReach, "网络连通性", "Network reachability"},
}

// Run 仅在库里不存在任何布局版本时执行：补齐种子实例（已有同插件实例则复用），
// 再把 8×5 种子布局写成第 1 版（来源 seed）。其余情况什么也不做。
func (s *Seeder) Run(ctx context.Context) error {
	cur, err := s.cfg.Layouts.Current(ctx)
	if err != nil {
		return fmt.Errorf("读取当前布局: %w", err)
	}
	if cur.Version != 0 {
		return nil
	}
	if err := s.ensureInstances(ctx); err != nil {
		return err
	}
	layout, ok := s.Layout(defaultGrid)
	if !ok {
		return fmt.Errorf("无法生成网格 %dx%d 的种子布局", defaultGrid.Cols, defaultGrid.Rows)
	}
	if _, err := s.cfg.Layouts.Save(ctx, 0, layout, screens.SaveOptions{Source: model.LayoutSourceSeed}); err != nil {
		return fmt.Errorf("写入种子布局: %w", err)
	}
	return nil
}

func (s *Seeder) ensureInstances(ctx context.Context) error {
	have, err := s.earliestByPlugin(ctx)
	if err != nil {
		return err
	}
	for _, sp := range instanceSpecs {
		if _, ok := have[sp.plugin]; ok {
			continue
		}
		name := sp.zh
		if s.lang() == "en" {
			name = sp.en
		}
		// 配置留空：各插件的默认值（如 net-reach 的探测目标、直连）由 manifest 的 default 提供。
		if _, err := s.cfg.Instances.Create(ctx, model.InstanceInput{PluginID: sp.plugin, Name: name, Config: map[string]any{}}); err != nil {
			return fmt.Errorf("创建种子实例 %s: %w", sp.plugin, err)
		}
	}
	return nil
}

// earliestByPlugin 返回每个插件最早创建的实例 id（占位绑定也取最早的，两者一致）。
func (s *Seeder) earliestByPlugin(ctx context.Context) (map[string]string, error) {
	list, err := s.cfg.Instances.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取实例列表: %w", err)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].CreatedAt.Before(list[j].CreatedAt)
		}
		return list[i].ID < list[j].ID
	})
	out := map[string]string{}
	for _, in := range list {
		if _, ok := out[in.PluginID]; !ok {
			out[in.PluginID] = in.ID
		}
	}
	return out, nil
}

func (s *Seeder) lang() string {
	if s.cfg.Language != nil && s.cfg.Language() == "en" {
		return "en"
	}
	return "zh"
}

// Layout 按网格返回种子布局，实例引用取调用时各插件最早创建的实例；该网格没有种子时 ok 为 false。
// 对应实例已被删除时，插件小组件退回占位绑定，聚合小组件整个省略，保证布局始终可保存。
// 它同时是 screens.SeedFunc，装配时经 UseSeedLayouts 注入。
func (s *Seeder) Layout(grid model.Grid) (model.Layout, bool) {
	place, ok := placements[grid]
	if !ok {
		return model.Layout{}, false
	}
	ids, err := s.earliestByPlugin(context.Background())
	if err != nil {
		// 读不到实例时宁可不给种子，也不生成一份聚合缺失、全是占位的残缺布局；
		// 启动路径（Run）与自动选网格路径都因此保持一致：本次不换布局，下次再试。
		slog.Warn("读取实例列表失败，本次不提供种子布局", "grid", fmt.Sprintf("%dx%d", grid.Cols, grid.Rows), "err", err)
		return model.Layout{}, false
	}
	home, reachTitle := "首页", "网络连通总览"
	if s.lang() == "en" {
		home, reachTitle = "Home", "Network overview"
	}
	widgets := make([]model.LayoutWidget, 0, len(place))
	for _, p := range place {
		w := model.LayoutWidget{
			ID: p.id, Size: model.WidgetSize{Cols: p.w, Rows: p.h}, Col: p.col, Row: p.row,
			Options: map[string]any{},
		}
		if p.aggregate {
			id, ok := ids[pluginReach]
			if !ok {
				continue
			}
			w.Source, w.Template = model.WidgetSourceAggregate, p.template
			w.Binding.Refs = []model.WidgetRef{{InstanceID: id, Item: "target[*]"}}
			w.Options["title"] = reachTitle
		} else {
			w.Source, w.PluginID, w.WidgetID = model.WidgetSourcePlugin, p.plugin, p.widget
			w.Binding.InstanceID = ids[p.plugin] // 实例不存在时为空，即占位
		}
		widgets = append(widgets, w)
	}
	return model.Layout{Grid: grid, Screens: []model.LayoutScreen{
		{ID: model.IndexScreenID, Name: home, InRotation: true, Widgets: widgets},
	}}, true
}
