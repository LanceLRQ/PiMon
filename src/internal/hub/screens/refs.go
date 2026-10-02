package screens

import (
	"context"
	"slices"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// ReferencedInstanceIDs 返回解析后布局引用到的实例 id（去重、升序）：
// 包含占位小组件绑定到的实例，不含失效引用。
func ReferencedInstanceIDs(l model.ResolvedLayout) []string {
	seen := map[string]bool{}
	for _, sc := range l.Screens {
		for _, w := range sc.Widgets {
			if w.InstanceID != "" {
				seen[w.InstanceID] = true
			}
			for _, refs := range w.Slots {
				for _, r := range refs {
					if r.InstanceID != "" {
						seen[r.InstanceID] = true
					}
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// InstanceReferenced 判断当前布局（按解析后的结果，含占位绑定）是否引用了该实例。
func (s *Service) InstanceReferenced(ctx context.Context, instanceID string) (bool, error) {
	l, err := s.Resolve(ctx, "")
	if err != nil {
		return false, err
	}
	_, found := slices.BinarySearch(ReferencedInstanceIDs(l), instanceID)
	return found, nil
}
