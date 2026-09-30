package manifest

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/i18n"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/internal/yamlnode"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

func decodeText(n *yaml.Node) (I18nText, string) { return i18n.Decode(n) }

// refChecker 判断 bind/alert 引用的 item 是否已在 outputs 中声明，并给出其数据项类型。
type refChecker struct {
	exact    map[string]string // 固定 key → 类型
	prefixes map[string]string // 动态集合前缀（"disk[*]" 或 "disk" 声明）→ 类型
}

func newRefChecker(outputs []Output) *refChecker {
	r := &refChecker{exact: map[string]string{}, prefixes: map[string]string{}}
	for _, o := range outputs {
		if base, ok := strings.CutSuffix(o.Key, "[*]"); ok {
			r.prefixes[base] = o.Type
			continue
		}
		r.exact[o.Key] = o.Type
		r.prefixes[o.Key] = o.Type
	}
	return r
}

// typeOf 返回引用的数据项类型；未声明时 ok 为 false。
func (r *refChecker) typeOf(item string) (typ string, ok bool) {
	if t, found := r.exact[item]; found {
		return t, true
	}
	if i := strings.Index(item, "["); i > 0 && strings.HasSuffix(item, "]") {
		t, found := r.prefixes[item[:i]]
		return t, found
	}
	return "", false
}

func (r *refChecker) declared(item string) bool {
	_, ok := r.typeOf(item)
	return ok
}

// checkField 校验引用的 field（非空时）属于数据项类型的字段集；类型本身不合法时由 outputs 校验报告，这里跳过。
func (p *parser) checkField(refs *refChecker, item, field string, node *yaml.Node, path string) {
	if field == "" {
		return
	}
	typ, _ := refs.typeOf(item)
	if !report.IsType(typ) {
		return
	}
	if !report.ValidField(typ, field) {
		if len(report.FieldsOf(typ)) == 0 {
			p.add(node, path, "类型 %s 没有可引用的字段，不能指定 field %q", typ, field)
			return
		}
		p.add(node, path, "字段 %q 不属于类型 %s，可用：%s", field, typ, strings.Join(report.FieldsOf(typ), "、"))
	}
}

func (p *parser) parseOutputs(n *yaml.Node) []Output {
	items, ok := yamlnode.Items(n)
	if !ok {
		p.add(n, "outputs", "outputs 必须是列表")
		return nil
	}
	var out []Output
	seen := map[string]bool{}
	for i, it := range items {
		path := fmt.Sprintf("outputs[%d]", i)
		pairs, ok := yamlnode.Pairs(it)
		if !ok {
			p.add(it, path, "必须是映射")
			continue
		}
		o := Output{Line: it.Line}
		for _, pr := range pairs {
			ppath := path + "." + pr.Key
			switch pr.Key {
			case "key":
				o.Key, _ = p.scalar(pr.Value, ppath)
			case "type":
				o.Type, _ = p.scalar(pr.Value, ppath)
			case "title":
				o.Title = p.text(pr.Value, ppath)
			default:
				p.add(pr.KeyNode, ppath, "未知字段 %s", pr.Key)
			}
		}
		if o.Key == "" {
			p.add(it, path+".key", "缺少 key")
			continue
		}
		if k, err := report.ParseKey(o.Key); err != nil {
			p.add(it, path+".key", "数据项 key %q 不合法: %v", o.Key, err)
			continue
		} else if k.Dynamic && !k.Wildcard {
			p.add(it, path+".key", "数据项 key %q 不能声明具体成员，动态集合请写成 name[*]", o.Key)
			continue
		}
		if !report.IsType(o.Type) {
			p.add(it, path+".type", "数据项类型 %q 不合法，应为 %s", o.Type, strings.Join(OutputTypes, "、"))
		}
		if seen[o.Key] {
			p.add(it, path+".key", "数据项 key %q 重复", o.Key)
			continue
		}
		seen[o.Key] = true
		out = append(out, o)
	}
	return out
}

func (p *parser) parseWidgets(n *yaml.Node, refs *refChecker) []Widget {
	items, ok := yamlnode.Items(n)
	if !ok {
		p.add(n, "widgets", "widgets 必须是列表")
		return nil
	}
	var out []Widget
	seen := map[string]bool{}
	for i, it := range items {
		path := fmt.Sprintf("widgets[%d]", i)
		pairs, ok := yamlnode.Pairs(it)
		if !ok {
			p.add(it, path, "必须是映射")
			continue
		}
		w := Widget{Line: it.Line}
		hasSizes := false
		for _, pr := range pairs {
			ppath := path + "." + pr.Key
			switch pr.Key {
			case "id":
				w.ID, _ = p.scalar(pr.Value, ppath)
			case "name":
				w.Name = p.text(pr.Value, ppath)
			case "sizes":
				hasSizes = true
				w.Sizes = p.parseSizes(pr.Value, ppath, refs)
			default:
				p.add(pr.KeyNode, ppath, "未知字段 %s", pr.Key)
			}
		}
		switch {
		case w.ID == "":
			p.add(it, path+".id", "缺少 id")
		case seen[w.ID]:
			p.add(it, path+".id", "小组件 id %q 重复", w.ID)
		}
		seen[w.ID] = true
		if !hasSizes {
			p.add(it, path+".sizes", "缺少 sizes")
		}
		out = append(out, w)
	}
	return out
}

func (p *parser) parseSizes(n *yaml.Node, path string, refs *refChecker) []WidgetSize {
	pairs, ok := yamlnode.Pairs(n)
	if !ok || len(pairs) == 0 {
		p.add(n, path, "sizes 必须是非空映射，键形如 2x1")
		return nil
	}
	var out []WidgetSize
	for _, pr := range pairs {
		spath := path + "." + pr.Key
		if pr.Duplicate {
			p.add(pr.KeyNode, spath, "尺寸 %s 重复", pr.Key)
			continue
		}
		ws := WidgetSize{Size: pr.Key, Line: pr.KeyNode.Line}
		m := sizePattern.FindStringSubmatch(pr.Key)
		if m == nil {
			p.add(pr.KeyNode, spath, "尺寸 %q 格式错误，应为 NxM（如 2x1）", pr.Key)
			continue
		}
		ws.Cols, _ = strconv.Atoi(m[1])
		ws.Rows, _ = strconv.Atoi(m[2])
		if ws.Cols < 1 || ws.Cols > MaxCols || ws.Rows < 1 || ws.Rows > MaxRows {
			p.add(pr.KeyNode, spath, "尺寸 %s 超出范围，列 1–%d、行 1–%d", pr.Key, MaxCols, MaxRows)
			continue
		}
		body, ok := yamlnode.Pairs(pr.Value)
		if !ok {
			p.add(pr.Value, spath, "必须是 {template, bind} 映射")
			continue
		}
		for _, b := range body {
			bpath := spath + "." + b.Key
			switch b.Key {
			case "template":
				if s, ok := p.scalar(b.Value, bpath); ok {
					ws.Template = s
					if !slices.Contains(Templates, s) {
						p.add(b.Value, bpath, "未知模板 %q，允许：%s", s, strings.Join(Templates, "、"))
					}
				}
			case "bind":
				ws.Bind = p.parseBind(b.Value, bpath, refs)
			default:
				p.add(b.KeyNode, bpath, "未知字段 %s", b.Key)
			}
		}
		if ws.Template == "" {
			p.add(pr.Value, spath+".template", "缺少 template")
		}
		out = append(out, ws)
	}
	return out
}

func (p *parser) parseBind(n *yaml.Node, path string, refs *refChecker) []Binding {
	pairs, ok := yamlnode.Pairs(n)
	if !ok {
		p.add(n, path, "bind 必须是 {槽名: 引用} 映射")
		return nil
	}
	var out []Binding
	for _, pr := range pairs {
		bpath := path + "." + pr.Key
		b := Binding{Slot: pr.Key}
		if items, isList := yamlnode.Items(pr.Value); isList {
			b.List = true
			for j, it := range items {
				if r, ok := p.parseRef(it, fmt.Sprintf("%s[%d]", bpath, j), refs); ok {
					b.Refs = append(b.Refs, r)
				}
			}
		} else if r, ok := p.parseRef(pr.Value, bpath, refs); ok {
			b.Refs = []ItemRef{r}
		}
		out = append(out, b)
	}
	return out
}

func (p *parser) parseRef(n *yaml.Node, path string, refs *refChecker) (ItemRef, bool) {
	pairs, ok := yamlnode.Pairs(n)
	if !ok {
		p.add(n, path, "引用必须是 {item, field} 映射")
		return ItemRef{}, false
	}
	var r ItemRef
	var itemNode, fieldNode *yaml.Node
	for _, pr := range pairs {
		switch pr.Key {
		case "item":
			r.Item, _ = p.scalar(pr.Value, path+".item")
			itemNode = pr.Value
		case "field":
			r.Field, _ = p.scalar(pr.Value, path+".field")
			fieldNode = pr.Value
		default:
			p.add(pr.KeyNode, path+"."+pr.Key, "未知字段 %s", pr.Key)
		}
	}
	if r.Item == "" {
		p.add(n, path+".item", "缺少 item")
		return r, false
	}
	if !refs.declared(r.Item) {
		p.add(itemNode, path+".item", "引用了未在 outputs 中声明的数据项 %q", r.Item)
		return r, false
	}
	p.checkField(refs, r.Item, r.Field, fieldNode, path+".field")
	return r, true
}

func (p *parser) parseAlerts(n *yaml.Node, refs *refChecker) []Alert {
	items, ok := yamlnode.Items(n)
	if !ok {
		p.add(n, "alerts", "alerts 必须是列表")
		return nil
	}
	var out []Alert
	for i, it := range items {
		path := fmt.Sprintf("alerts[%d]", i)
		pairs, ok := yamlnode.Pairs(it)
		if !ok {
			p.add(it, path, "必须是映射")
			continue
		}
		a := Alert{Line: it.Line}
		var itemNode, fieldNode, opNode, sevNode *yaml.Node
		for _, pr := range pairs {
			ppath := path + "." + pr.Key
			switch pr.Key {
			case "name":
				a.Name = p.text(pr.Value, ppath)
			case "item":
				a.Item, _ = p.scalar(pr.Value, ppath)
				itemNode = pr.Value
			case "field":
				a.Field, _ = p.scalar(pr.Value, ppath)
				fieldNode = pr.Value
			case "op":
				a.Op, _ = p.scalar(pr.Value, ppath)
				opNode = pr.Value
			case "value":
				var v any
				if err := pr.Value.Decode(&v); err != nil {
					p.add(pr.Value, ppath, "value 无法解析")
				}
				a.Value = v
			case "severity":
				a.Severity, _ = p.scalar(pr.Value, ppath)
				sevNode = pr.Value
			case "for":
				a.For, _ = p.duration(pr.Value, ppath)
			default:
				p.add(pr.KeyNode, ppath, "未知字段 %s", pr.Key)
			}
		}
		if a.Name.IsZero() {
			p.add(it, path+".name", "缺少 name")
		}
		if a.Item == "" {
			p.add(it, path+".item", "缺少 item")
		} else if !refs.declared(a.Item) {
			p.add(itemNode, path+".item", "引用了未在 outputs 中声明的数据项 %q", a.Item)
		} else {
			p.checkField(refs, a.Item, a.Field, fieldNode, path+".field")
		}
		if !operators[a.Op] {
			p.add(nodeOr(opNode, it), path+".op", "op %q 不合法，应为 <、<=、>、>=、==、!=", a.Op)
		}
		if a.Severity != "" && !severities[a.Severity] {
			p.add(sevNode, path+".severity", "severity %q 不合法，应为 info、warning、critical", a.Severity)
		}
		out = append(out, a)
	}
	return out
}

func nodeOr(n, fallback *yaml.Node) *yaml.Node {
	if n != nil {
		return n
	}
	return fallback
}
