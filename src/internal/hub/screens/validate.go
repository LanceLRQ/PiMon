package screens

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

// 布局规模与字段的上下限。
const (
	MaxGridCols       = 12
	MaxGridRows       = 8
	MaxScreens        = 32
	MaxAggregateRefs  = 64
	MinDwellSeconds   = 3
	MaxDwellSeconds   = 3600
	maxNameRunes      = 64
	maxTitleRunes     = 64
	maxIconRunes      = 48
	maxOptionsBytes   = 8192
	maxWidgetsPerPage = MaxGridCols * MaxGridRows
)

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	iconPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// snapshot 是一次校验或解析所依据的实例事实。
type snapshot struct {
	instances map[string]model.Instance
	// earliest 是插件 id → 该插件最早创建的实例 id（占位绑定用）。
	earliest map[string]string
}

func newSnapshot(list []model.Instance) *snapshot {
	sn := &snapshot{instances: make(map[string]model.Instance, len(list)), earliest: map[string]string{}}
	for _, in := range list {
		sn.instances[in.ID] = in
		cur, ok := sn.earliest[in.PluginID]
		if !ok {
			sn.earliest[in.PluginID] = in.ID
			continue
		}
		c := sn.instances[cur]
		if in.CreatedAt.Before(c.CreatedAt) || (in.CreatedAt.Equal(c.CreatedAt) && in.ID < cur) {
			sn.earliest[in.PluginID] = in.ID
		}
	}
	return sn
}

// normalize 把 nil 切片与 nil 映射规整为空值，使序列化结果稳定、前端无需判空。
func normalize(l *model.Layout) {
	if l.Screens == nil {
		l.Screens = []model.LayoutScreen{}
	}
	for i := range l.Screens {
		sc := &l.Screens[i]
		sc.Name = strings.TrimSpace(sc.Name)
		if sc.Widgets == nil {
			sc.Widgets = []model.LayoutWidget{}
		}
		for j := range sc.Widgets {
			if sc.Widgets[j].Options == nil {
				sc.Widgets[j].Options = map[string]any{}
			}
		}
	}
}

// cloneLayout 经 JSON 往返得到独立副本，同时规整空值。
func cloneLayout(l model.Layout) (model.Layout, error) {
	raw, err := json.Marshal(l)
	if err != nil {
		return model.Layout{}, err
	}
	var out model.Layout
	if err := json.Unmarshal(raw, &out); err != nil {
		return model.Layout{}, err
	}
	normalize(&out)
	return out, nil
}

func invalidAt(screen, widget, field string) model.LayoutProblem {
	return model.LayoutProblem{Screen: screen, Widget: widget, Code: model.LayoutProblemInvalid, Field: field}
}

func manifestSizes(sizes []manifest.WidgetSize) []string {
	out := make([]string, 0, len(sizes))
	for _, s := range sizes {
		out = append(out, sizeKey(s.Cols, s.Rows))
	}
	return out
}

func catalogKeys(sizes []model.WidgetSize) []string {
	out := make([]string, 0, len(sizes))
	for _, s := range sizes {
		out = append(out, sizeKey(s.Cols, s.Rows))
	}
	return out
}

// findWidget 在 manifest 里按 id 找小组件。
func findWidget(m *manifest.Manifest, id string) (manifest.Widget, bool) {
	for _, w := range m.Widgets {
		if w.ID == id {
			return w, true
		}
	}
	return manifest.Widget{}, false
}

// check 校验并规整 l（原地修改，调用方传入副本）。problems 非空时布局不可保存；
// broken 是引用失效的小组件，仅作提示。
func (s *Service) check(l *model.Layout, sn *snapshot) (problems, broken []model.LayoutProblem) {
	normalize(l)
	problems, broken = []model.LayoutProblem{}, []model.LayoutProblem{}

	g := l.Grid
	if g.Cols < 1 || g.Cols > MaxGridCols || g.Rows < 1 || g.Rows > MaxGridRows {
		problems = append(problems, invalidAt("", "", "grid"))
	}
	if len(l.Screens) < 1 || len(l.Screens) > MaxScreens {
		problems = append(problems, invalidAt("", "", "screens"))
	}

	screenIDs := map[string]bool{}
	widgetIDs := map[string]bool{}
	for si := range l.Screens {
		sc := &l.Screens[si]
		path := fmt.Sprintf("screens[%d]", si)
		if !idPattern.MatchString(sc.ID) || screenIDs[sc.ID] {
			problems = append(problems, invalidAt(sc.ID, "", path+".id"))
		}
		screenIDs[sc.ID] = true
		if n := utf8.RuneCountInString(sc.Name); n < 1 || n > maxNameRunes {
			problems = append(problems, invalidAt(sc.ID, "", path+".name"))
		}
		if sc.DwellSeconds != 0 && (sc.DwellSeconds < MinDwellSeconds || sc.DwellSeconds > MaxDwellSeconds) {
			problems = append(problems, invalidAt(sc.ID, "", path+".dwell_seconds"))
		}
		if len(sc.Widgets) > maxWidgetsPerPage {
			problems = append(problems, invalidAt(sc.ID, "", path+".widgets"))
			continue
		}

		placements := make([]Placement, 0, len(sc.Widgets))
		for wi := range sc.Widgets {
			w := &sc.Widgets[wi]
			wpath := fmt.Sprintf("%s.widgets[%d]", path, wi)
			if !idPattern.MatchString(w.ID) || widgetIDs[w.ID] {
				problems = append(problems, invalidAt(sc.ID, w.ID, wpath+".id"))
			}
			widgetIDs[w.ID] = true
			p := Placement{ID: w.ID, Col: w.Col, Row: w.Row, W: w.Size.Cols, H: w.Size.Rows}
			ps, bs, allowed := s.checkWidget(sc.ID, w, wpath, sn)
			problems = append(problems, ps...)
			broken = append(broken, bs...)
			p.Allowed = allowed
			placements = append(placements, p)
		}
		for _, gp := range CheckGeometry(g, placements) {
			gp.Screen = sc.ID
			problems = append(problems, gp)
		}
	}
	if !screenIDs[model.IndexScreenID] {
		problems = append(problems, invalidAt("", "", "screens.index"))
	}
	return problems, broken
}

// checkWidget 做单个小组件与来源有关的校验，返回结构问题、引用失效，以及该小组件允许的尺寸集合
// （nil 表示无法确定、不限制，例如插件已消失）。
func (s *Service) checkWidget(screenID string, w *model.LayoutWidget, path string, sn *snapshot) (problems, broken []model.LayoutProblem, allowed []string) {
	bad := func(field string) { problems = append(problems, invalidAt(screenID, w.ID, path+"."+field)) }
	brk := func(code string) {
		broken = append(broken, model.LayoutProblem{Screen: screenID, Widget: w.ID, Code: code})
	}

	if !validOptions(w.Options) {
		bad("options")
	}

	switch w.Source {
	case model.WidgetSourcePlugin:
		if w.PluginID == "" || w.WidgetID == "" {
			bad("plugin_id")
		}
		if w.Template != "" {
			bad("template")
		}
		if len(w.Binding.Refs) > 0 {
			bad("binding.refs")
		}
		if p, ok := s.reg.Get(w.PluginID); !ok || p.Manifest == nil {
			if w.PluginID != "" {
				brk(model.LayoutBrokenPluginMissing)
			}
		} else if mw, found := findWidget(p.Manifest, w.WidgetID); !found {
			brk(model.LayoutBrokenWidgetMissing)
		} else {
			allowed = manifestSizes(mw.Sizes)
		}
		if id := w.Binding.InstanceID; id != "" {
			if in, ok := sn.instances[id]; !ok {
				brk(model.LayoutBrokenInstanceMissing)
			} else if in.PluginID != w.PluginID {
				brk(model.LayoutBrokenInstancePlugin)
			}
		}

	case model.WidgetSourceGeneric, model.WidgetSourceAggregate:
		if w.PluginID != "" || w.WidgetID != "" {
			bad("plugin_id")
		}
		if w.Binding.InstanceID != "" {
			bad("binding.instance_id")
		}
		if sizes, ok := catalogSizes(w.Source, w.Template); !ok {
			bad("template")
		} else {
			allowed = catalogKeys(sizes)
		}
		limit := 1
		if w.Source == model.WidgetSourceAggregate {
			limit = MaxAggregateRefs
		}
		if len(w.Binding.Refs) > limit {
			bad("binding.refs")
		}
		missing := false
		for i, r := range w.Binding.Refs {
			if r.InstanceID == "" || r.Item == "" {
				bad(fmt.Sprintf("binding.refs[%d]", i))
				continue
			}
			if _, ok := sn.instances[r.InstanceID]; !ok {
				missing = true
			}
		}
		if missing {
			brk(model.LayoutBrokenInstanceMissing)
		}

	default:
		bad("source")
	}
	return problems, broken, allowed
}

// validOptions 只校验 title 与 icon 的类型和长度，并限制整体大小；其余键原样保存。
func validOptions(o map[string]any) bool {
	if v, ok := o["title"]; ok {
		t, isStr := v.(string)
		if !isStr || utf8.RuneCountInString(t) > maxTitleRunes {
			return false
		}
	}
	if v, ok := o["icon"]; ok {
		t, isStr := v.(string)
		if !isStr || utf8.RuneCountInString(t) > maxIconRunes || (t != "" && !iconPattern.MatchString(t)) {
			return false
		}
	}
	raw, err := json.Marshal(o)
	return err == nil && len(raw) <= maxOptionsBytes
}

// checkStored 检查已存储的布局：只关心引用失效。尺寸已不被插件或目录支持的小组件
// 也算失效（size_unsupported），因为存储时它是合法的，之后插件升级才变了。
func (s *Service) checkStored(l *model.Layout, sn *snapshot) []model.LayoutProblem {
	problems, broken := s.check(l, sn)
	for _, p := range problems {
		if p.Code == model.LayoutProblemSizeNotAllowed {
			broken = append(broken, model.LayoutProblem{Screen: p.Screen, Widget: p.Widget, Code: model.LayoutBrokenSizeUnsupported})
		}
	}
	return broken
}
