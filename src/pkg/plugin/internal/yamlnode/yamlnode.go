// Package yamlnode 是 manifest 与 schema 共用的 yaml.Node 遍历辅助。
package yamlnode

import "go.yaml.in/yaml/v3"

// Pair 是映射节点里的一个键值对，保留键节点以便报告行号。
type Pair struct {
	Key       string
	KeyNode   *yaml.Node
	Value     *yaml.Node
	Duplicate bool // 该键此前已出现过
}

// Resolve 跟随别名节点。
func Resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// Pairs 按出现顺序返回映射节点的键值对；节点不是映射时返回 ok=false。
func Pairs(n *yaml.Node) (pairs []Pair, ok bool) {
	n = Resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, false
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		pairs = append(pairs, Pair{Key: k.Value, KeyNode: k, Value: Resolve(n.Content[i+1]), Duplicate: seen[k.Value]})
		seen[k.Value] = true
	}
	return pairs, true
}

// Items 返回序列节点的元素；节点不是序列时返回 ok=false。
func Items(n *yaml.Node) ([]*yaml.Node, bool) {
	n = Resolve(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil, false
	}
	out := make([]*yaml.Node, len(n.Content))
	for i, c := range n.Content {
		out[i] = Resolve(c)
	}
	return out, true
}
