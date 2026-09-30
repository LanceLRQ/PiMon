// Package i18n 提供插件 manifest 中面向用户的多语言文本。
package i18n

import "go.yaml.in/yaml/v3"

// Text 是中英双语文本。manifest 中可写成 {zh: …, en: …} 或单个字符串，
// 缺失的语言回退到另一种。
type Text struct {
	ZH string `json:"zh"`
	EN string `json:"en"`
}

// Get 按语言取值：zh 取中文、其余取英文，对应语言为空时回退到另一种。
func (t Text) Get(lang string) string {
	if lang == "zh" {
		if t.ZH != "" {
			return t.ZH
		}
		return t.EN
	}
	if t.EN != "" {
		return t.EN
	}
	return t.ZH
}

// IsZero 报告两种语言是否都为空。
func (t Text) IsZero() bool { return t.ZH == "" && t.EN == "" }

// Decode 从 YAML 节点解析多语言文本；失败时返回非空的错误说明（行号由调用方从节点取）。
func Decode(n *yaml.Node) (Text, string) {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return Text{ZH: n.Value, EN: n.Value}, ""
	case yaml.MappingNode:
		var t Text
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i].Value, n.Content[i+1]
			if v.Kind != yaml.ScalarNode {
				return Text{}, "多语言文本的值必须是字符串"
			}
			switch k {
			case "zh":
				t.ZH = v.Value
			case "en":
				t.EN = v.Value
			default:
				return Text{}, "多语言文本只支持 zh、en，未知语言 " + k
			}
		}
		if t.IsZero() {
			return Text{}, "多语言文本不能为空"
		}
		if t.ZH == "" {
			t.ZH = t.EN
		}
		if t.EN == "" {
			t.EN = t.ZH
		}
		return t, ""
	default:
		return Text{}, "文本必须是字符串或 {zh, en} 映射"
	}
}
