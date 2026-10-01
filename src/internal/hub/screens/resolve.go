package screens

import (
	"context"
	"strings"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// 聚合小组件取最严重状态时的排序，数值越大越严重；未列出的状态按 unknown 处理。
var severity = map[string]int{
	string(report.DisplayOK):          0,
	string(report.DisplayUnknown):     1,
	string(report.DisplayMaintenance): 2,
	string(report.DisplayOffline):     3,
	string(report.DisplayStale):       4,
	string(report.DisplayWarning):     5,
	string(report.DisplayError):       6,
	string(report.DisplayCritical):    7,
}

// Resolve 返回解析后的当前布局，供屏幕端渲染：每个小组件带模板、各槽解析出的数据项引用、
// 展示状态和按 lang 解析的标题。占位小组件（plugin 来源且未指定实例）在这里绑定到
// 该插件最早创建的实例，结果不写回布局、不产生新版本。
func (s *Service) Resolve(ctx context.Context, lang string) (model.ResolvedLayout, error) {
	st, err := s.Current(ctx)
	if err != nil {
		return model.ResolvedLayout{}, err
	}
	sn, err := s.snapshot(ctx)
	if err != nil {
		return model.ResolvedLayout{}, err
	}
	brokenSet := map[string]bool{}
	for _, b := range st.Broken {
		brokenSet[b.Screen+"/"+b.Widget] = true
	}

	out := model.ResolvedLayout{Version: st.Version, Grid: st.Layout.Grid, Screens: make([]model.ResolvedScreen, 0, len(st.Layout.Screens))}
	for _, sc := range st.Layout.Screens {
		rs := model.ResolvedScreen{
			ID: sc.ID, Name: sc.Name, DwellSeconds: sc.DwellSeconds, InRotation: sc.InRotation,
			Widgets: make([]model.ResolvedWidget, 0, len(sc.Widgets)),
		}
		for _, w := range sc.Widgets {
			rs.Widgets = append(rs.Widgets, s.resolveWidget(w, lang, sn, brokenSet[sc.ID+"/"+w.ID]))
		}
		out.Screens = append(out.Screens, rs)
	}
	return out, nil
}

func (s *Service) resolveWidget(w model.LayoutWidget, lang string, sn *snapshot, broken bool) model.ResolvedWidget {
	rw := model.ResolvedWidget{
		ID: w.ID, Source: w.Source, PluginID: w.PluginID, WidgetID: w.WidgetID, Template: w.Template,
		Size: w.Size, Col: w.Col, Row: w.Row, Options: w.Options,
		Slots: map[string][]model.ResolvedRef{},
	}
	title, _ := w.Options["title"].(string)
	title = strings.TrimSpace(title)

	switch w.Source {
	case model.WidgetSourcePlugin:
		s.resolvePlugin(&rw, w, lang, sn, broken, &title)
	case model.WidgetSourceGeneric:
		rw.DisplayState = refsState(w.Binding.Refs, sn, broken)
		if !broken && len(w.Binding.Refs) == 1 {
			rw.Slots["value"] = s.resolveRefs(w.Binding.Refs, lang, sn)
			if title == "" {
				title = s.itemTitle(w.Binding.Refs[0], lang, sn)
			}
		}
	case model.WidgetSourceAggregate:
		rw.DisplayState = refsState(w.Binding.Refs, sn, broken)
		if !broken && len(w.Binding.Refs) > 0 {
			rw.Slots["items"] = s.resolveRefs(w.Binding.Refs, lang, sn)
		}
	}
	rw.Title = title
	if rw.Options == nil {
		rw.Options = map[string]any{}
	}
	return rw
}

func (s *Service) resolvePlugin(rw *model.ResolvedWidget, w model.LayoutWidget, lang string, sn *snapshot, broken bool, title *string) {
	p, pluginOK := s.reg.Get(w.PluginID)
	var mw manifest.Widget
	widgetOK := false
	if pluginOK && p.Manifest != nil {
		mw, widgetOK = findWidget(p.Manifest, w.WidgetID)
	}
	if widgetOK && *title == "" {
		*title = mw.Name.Get(lang)
	}

	if broken {
		rw.DisplayState = string(report.DisplayBroken)
		return
	}
	instID := w.Binding.InstanceID
	if instID == "" {
		instID = sn.earliest[w.PluginID]
		rw.Placeholder = instID != ""
	}
	if instID == "" {
		rw.DisplayState = string(report.DisplayUnconfigured)
		return
	}
	rw.InstanceID = instID
	rw.DisplayState = sn.instances[instID].DisplayState

	for _, size := range mw.Sizes {
		if size.Cols != w.Size.Cols || size.Rows != w.Size.Rows {
			continue
		}
		rw.Template = size.Template
		for _, b := range size.Bind {
			refs := make([]model.WidgetRef, 0, len(b.Refs))
			for _, r := range b.Refs {
				refs = append(refs, model.WidgetRef{InstanceID: instID, Item: r.Item, Field: r.Field})
			}
			rw.Slots[b.Slot] = s.resolveRefs(refs, lang, sn)
		}
		break
	}
}

// resolveRefs 为每个引用补上数据项标题。
func (s *Service) resolveRefs(refs []model.WidgetRef, lang string, sn *snapshot) []model.ResolvedRef {
	out := make([]model.ResolvedRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, model.ResolvedRef{
			InstanceID: r.InstanceID, Item: r.Item, Field: r.Field, Title: s.refTitle(r, lang, sn),
		})
	}
	return out
}

// refTitle 取引用的数据项标题：键与 manifest outputs 完全相同取其 title；
// 键形如 base[name] 且 outputs 声明了 base[*] 时（动态成员）取方括号里的名字；其余为空串。
func (s *Service) refTitle(r model.WidgetRef, lang string, sn *snapshot) string {
	in, ok := sn.instances[r.InstanceID]
	if !ok {
		return ""
	}
	p, ok := s.reg.Get(in.PluginID)
	if !ok || p.Manifest == nil {
		return ""
	}
	for _, o := range p.Manifest.Outputs {
		if o.Key == r.Item {
			return o.Title.Get(lang)
		}
	}
	if open := strings.IndexByte(r.Item, '['); open > 0 && strings.HasSuffix(r.Item, "]") {
		pattern := r.Item[:open] + "[*]"
		for _, o := range p.Manifest.Outputs {
			if o.Key == pattern {
				return r.Item[open+1 : len(r.Item)-1]
			}
		}
	}
	return ""
}

// itemTitle 取 generic 小组件所绑定数据项在插件 manifest 里声明的标题，取不到返回空串。
func (s *Service) itemTitle(r model.WidgetRef, lang string, sn *snapshot) string {
	in, ok := sn.instances[r.InstanceID]
	if !ok {
		return ""
	}
	p, ok := s.reg.Get(in.PluginID)
	if !ok || p.Manifest == nil {
		return ""
	}
	for _, o := range p.Manifest.Outputs {
		if o.Key == r.Item {
			return o.Title.Get(lang)
		}
	}
	return ""
}

// refsState 计算 generic、aggregate 小组件的展示状态：没有引用为 unconfigured，
// 存在失效引用为 broken，否则取所引用实例里最严重的状态。
func refsState(refs []model.WidgetRef, sn *snapshot, broken bool) string {
	if broken {
		return string(report.DisplayBroken)
	}
	if len(refs) == 0 {
		return string(report.DisplayUnconfigured)
	}
	worst, rank := "", -1
	for _, r := range refs {
		st := sn.instances[r.InstanceID].DisplayState
		rk, ok := severity[st]
		if !ok {
			rk = severity[string(report.DisplayUnknown)]
		}
		if rk > rank {
			worst, rank = st, rk
		}
	}
	return worst
}
