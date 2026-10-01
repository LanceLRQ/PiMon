package report

import "slices"

// Merge 合并“上次展示用报告”与本次采集结果，得到当前展示用报告（供实例仓库使用）：
//   - err == nil 且 res != nil：本次成功，整份替换（全量快照，缺失的数据项视为已移除），stale=false，返回 res 本身；
//   - err != nil：本次失败，忽略 res，沿用 prev 的全部内容（含 status、summary、私有 state），
//     返回的副本中报告与每个数据项都标记 Stale，stale=true；不修改 prev，也不把旧值改写或清零；
//   - prev 为 nil 且失败：没有可展示的旧值，返回 nil, false，不得伪造数据（展示状态由 error 表达）。
//
// 失败本身用 ComputeDisplayState 的 LastFailed 表达，Merge 只负责保留旧值。
func Merge(prev, res *Report, err error) (cur *Report, stale bool) {
	if err == nil && res != nil {
		return res, false
	}
	if prev == nil {
		return nil, false
	}
	c := *prev
	c.Stale = true
	c.Items = slices.Clone(prev.Items)
	for i := range c.Items {
		c.Items[i].Stale = true
	}
	return &c, true
}
