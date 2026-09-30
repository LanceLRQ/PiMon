package manifest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/internal/yamlnode"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/schema"
)

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
	sizePattern    = regexp.MustCompile(`^(\d+)x(\d+)$`)
	yamlLineErr    = regexp.MustCompile(`line (\d+)`)
	operators      = map[string]bool{"<": true, "<=": true, ">": true, ">=": true, "==": true, "!=": true}
	severities     = map[string]bool{"info": true, "warning": true, "critical": true}
)

// Parse 解析并校验 plugin.yaml。失败时返回 *Error，列出全部问题及行号。
func Parse(data []byte) (*Manifest, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		p := Problem{Message: strings.TrimPrefix(err.Error(), "yaml: ")}
		if m := yamlLineErr.FindStringSubmatch(err.Error()); m != nil {
			p.Line, _ = strconv.Atoi(m[1])
		}
		return nil, &Error{Problems: []Problem{p}}
	}
	if len(doc.Content) == 0 {
		return nil, &Error{Problems: []Problem{{Message: "文件为空"}}}
	}
	p := &parser{}
	m := p.parseRoot(doc.Content[0])
	if len(p.problems) > 0 {
		e := &Error{Problems: p.problems}
		e.sort()
		return nil, e
	}
	return m, nil
}

type parser struct {
	problems []Problem
}

func (p *parser) add(n *yaml.Node, path, format string, args ...any) {
	line := 0
	if n != nil {
		line = n.Line
	}
	p.problems = append(p.problems, Problem{Line: line, Path: path, Message: fmt.Sprintf(format, args...)})
}

func (p *parser) scalar(n *yaml.Node, path string) (string, bool) {
	if n.Kind != yaml.ScalarNode {
		p.add(n, path, "必须是字符串")
		return "", false
	}
	return n.Value, true
}

func (p *parser) text(n *yaml.Node, path string) I18nText {
	t, msg := decodeText(n)
	if msg != "" {
		p.add(n, path, "%s", msg)
	}
	return t
}

func (p *parser) duration(n *yaml.Node, path string) (time.Duration, bool) {
	s, ok := p.scalar(n, path)
	if !ok {
		return 0, false
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		p.add(n, path, "%q 不是有效的正时长（如 30s、5m）", s)
		return 0, false
	}
	return d, true
}

func (p *parser) parseRoot(root *yaml.Node) *Manifest {
	pairs, ok := yamlnode.Pairs(root)
	if !ok {
		p.add(root, "", "顶层必须是映射")
		return nil
	}
	m := &Manifest{}
	present := map[string]bool{}
	var outputsNode, widgetsNode, alertsNode, minIntervalNode *yaml.Node
	for _, pr := range pairs {
		if pr.Duplicate {
			p.add(pr.KeyNode, pr.Key, "字段 %s 重复", pr.Key)
			continue
		}
		present[pr.Key] = true
		v := pr.Value
		switch pr.Key {
		case "id":
			if s, ok := p.scalar(v, "id"); ok {
				m.ID = s
				if !idPattern.MatchString(s) {
					p.add(v, "id", "id %q 不合法，只能用小写字母、数字和连字符", s)
				}
			}
		case "version":
			if s, ok := p.scalar(v, "version"); ok {
				m.Version = s
				if !versionPattern.MatchString(s) {
					p.add(v, "version", "version %q 不是 x.y.z 形式", s)
				}
			}
		case "api_version":
			s, ok := p.scalar(v, "api_version")
			n, err := strconv.Atoi(s)
			switch {
			case !ok:
			case err != nil:
				p.add(v, "api_version", "api_version 必须是整数")
			case n != SupportedAPIVersion:
				p.add(v, "api_version", "不支持 api_version %d，当前支持 %d", n, SupportedAPIVersion)
			default:
				m.APIVersion = n
			}
		case "name":
			m.Name = p.text(v, "name")
		case "kind":
			if s, ok := p.scalar(v, "kind"); ok {
				m.Kind = Kind(s)
				if m.Kind != KindSource && m.Kind != KindNotifier {
					p.add(v, "kind", "kind %q 不合法，应为 source 或 notifier", s)
				}
			}
		case "runtime":
			if s, ok := p.scalar(v, "runtime"); ok {
				m.Runtime = Runtime(s)
				if m.Runtime != RuntimeBuiltin && m.Runtime != RuntimeExec {
					p.add(v, "runtime", "runtime %q 不合法，应为 builtin 或 exec", s)
				}
			}
		case "runs_on":
			m.RunsOn = p.runsOn(v)
		case "interval":
			if d, ok := p.duration(v, "interval"); ok {
				m.Interval = d
				if d < MinAllowedInterval {
					p.add(v, "interval", "interval（%s）不能低于全局下限 %s", d, MinAllowedInterval)
				}
			}
		case "min_interval":
			m.MinInterval, _ = p.duration(v, "min_interval")
			minIntervalNode = v
		case "timeout":
			m.Timeout, _ = p.duration(v, "timeout")
		case "config_schema":
			fields, issues := schema.DecodeFields(v, "config_schema")
			m.ConfigSchema = fields
			p.problems = append(p.problems, issues...)
		case "outputs":
			outputsNode = v
		case "widgets":
			widgetsNode = v
		case "alerts":
			alertsNode = v
		default:
			p.add(pr.KeyNode, pr.Key, "未知字段 %s", pr.Key)
		}
	}
	for _, k := range []string{"id", "version", "api_version", "name", "kind", "runtime", "runs_on"} {
		if !present[k] {
			p.add(root, k, "缺少必填字段 %s", k)
		}
	}
	if m.Interval == 0 && !present["interval"] {
		m.Interval = DefaultInterval
	}
	if m.MinInterval > m.Interval && minIntervalNode != nil {
		p.add(minIntervalNode, "min_interval", "min_interval（%s）不能大于 interval（%s）", m.MinInterval, m.Interval)
	}
	if m.Timeout == 0 && !present["timeout"] {
		m.Timeout = DefaultTimeout
	}
	if outputsNode != nil {
		m.Outputs = p.parseOutputs(outputsNode)
	}
	refs := newRefChecker(m.Outputs)
	if widgetsNode != nil {
		m.Widgets = p.parseWidgets(widgetsNode, refs)
	}
	if alertsNode != nil {
		m.Alerts = p.parseAlerts(alertsNode, refs)
	}
	return m
}

func (p *parser) runsOn(n *yaml.Node) []string {
	items, ok := yamlnode.Items(n)
	if !ok || len(items) == 0 {
		p.add(n, "runs_on", "runs_on 必须是非空列表，取值 hub、agent")
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		s, ok := p.scalar(it, "runs_on")
		if !ok {
			continue
		}
		if s != RunsOnHub && s != RunsOnAgent {
			p.add(it, "runs_on", "runs_on 取值 %q 不合法，应为 hub 或 agent", s)
			continue
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
