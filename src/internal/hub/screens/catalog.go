package screens

import "github.com/LanceLRQ/PiMon/src/pkg/model"

func sz(cols, rows int) model.WidgetSize { return model.WidgetSize{Cols: cols, Rows: rows} }

// 通用小组件目录：模板 → 允许的尺寸（顺序即向前端展示的顺序）。
// 通用小组件把任一实例的某个数据项套上通用模板，绑定在布局的 binding.refs 里（至多一项）。
var genericCatalog = []model.CatalogEntry{
	{Template: "value", Sizes: []model.WidgetSize{sz(1, 1), sz(2, 1), sz(2, 2)}},
	{Template: "gauge", Sizes: []model.WidgetSize{sz(1, 1), sz(2, 1), sz(2, 2)}},
	{Template: "state", Sizes: []model.WidgetSize{sz(1, 1), sz(2, 1)}},
	{Template: "chart", Sizes: []model.WidgetSize{sz(2, 1), sz(2, 2), sz(4, 2)}},
	{Template: "list", Sizes: []model.WidgetSize{sz(2, 2), sz(2, 3), sz(4, 2), sz(4, 3)}},
	{Template: "table", Sizes: []model.WidgetSize{sz(2, 2), sz(4, 2), sz(4, 3)}},
}

// 聚合小组件目录：把多个实例的数据项汇总到一个模板，绑定在 binding.refs 里。
var aggregateCatalog = []model.CatalogEntry{
	{Template: "status-grid", Sizes: []model.WidgetSize{sz(2, 2), sz(4, 2), sz(4, 3), sz(6, 2)}},
}

func cloneEntries(in []model.CatalogEntry) []model.CatalogEntry {
	out := make([]model.CatalogEntry, len(in))
	for i, e := range in {
		out[i] = model.CatalogEntry{Template: e.Template, Sizes: append([]model.WidgetSize(nil), e.Sizes...)}
	}
	return out
}

// Catalog 返回通用与聚合小组件目录的副本。
func (s *Service) Catalog() model.WidgetCatalog {
	return model.WidgetCatalog{Generic: cloneEntries(genericCatalog), Aggregate: cloneEntries(aggregateCatalog)}
}

// catalogSizes 返回目录里某模板允许的尺寸，模板不在目录里时 ok 为 false。
func catalogSizes(source, template string) (sizes []model.WidgetSize, ok bool) {
	list := genericCatalog
	if source == model.WidgetSourceAggregate {
		list = aggregateCatalog
	}
	for _, e := range list {
		if e.Template == template {
			return e.Sizes, true
		}
	}
	return nil, false
}
