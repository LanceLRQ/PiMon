package runtime

import (
	"fmt"
	"sort"
	"sync"
)

var (
	regMu      sync.RWMutex
	registered = map[string]Source{}
)

// Register 注册内置插件，供各插件包的 init 调用。id 重复会 panic。
func Register(s Source) {
	id := s.Manifest().ID
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registered[id]; dup {
		panic(fmt.Sprintf("内置插件 id 重复注册: %s", id))
	}
	registered[id] = s
}

// Builtins 按 id 升序返回全部已注册的内置插件。
func Builtins() []Source {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Source, 0, len(registered))
	for _, s := range registered {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest().ID < out[j].Manifest().ID })
	return out
}

// Builtin 按 id 取内置插件。
func Builtin(id string) (Source, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	s, ok := registered[id]
	return s, ok
}
