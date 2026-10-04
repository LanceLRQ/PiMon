package screens

import (
	"context"
	"fmt"

	"github.com/LanceLRQ/PiMon/src/internal/hub/plugins"
	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// 主机灯组每格 CPU 读数的来源：host-metrics 插件的 cpu 数据项。
const (
	hostMetricsPlugin = "host-metrics"
	hostCPUItem       = "cpu"
)

// hasHostsWidget 判断布局里是否有主机灯组；没有时解析不读设备列表。
func hasHostsWidget(l model.Layout) bool {
	for _, sc := range l.Screens {
		for _, w := range sc.Widgets {
			if w.Source == model.WidgetSourceAggregate && w.Binding.DataSource == model.AggregateSourceHosts {
				return true
			}
		}
	}
	return false
}

// hostCells 生成主机灯组的各格。规则（编辑器草稿解析须照此复刻，前后端一致）：
//   - 中枢固定第一，状态恒为 online；其后按设备创建顺序（设备列表的顺序），只收已确认的设备，
//     占位（未握手、重新配对中）不进灯组；
//   - 每台机器的 CPU 来源取 runs_on 等于它、创建最早（同时刻按 id 升序）的 host-metrics 实例，
//     暂停的也算，与占位绑定取最早实例同口径；没有则 InstanceID 为空；
//   - 状态见 hostState。
//
// 读设备失败返回错误，由调用方等下一轮再解析，不推一份缺了设备的灯组。
func (s *Service) hostCells(ctx context.Context, sn *snapshot) ([]model.ResolvedHost, error) {
	var devs []model.Device
	if s.devs != nil {
		var err error
		if devs, err = s.devs.List(ctx); err != nil {
			return nil, fmt.Errorf("读取设备列表: %w", err)
		}
	}
	cpu := map[string]model.Instance{}
	for _, in := range sn.instances {
		if in.PluginID != hostMetricsPlugin {
			continue
		}
		m := machineOf(in)
		if cur, ok := cpu[m]; !ok || earlier(in, cur) {
			cpu[m] = in
		}
	}
	out := make([]model.ResolvedHost, 0, len(devs)+1)
	out = append(out, model.ResolvedHost{ID: plugins.MachineHub, State: model.PresenceOnline, InstanceID: cpu[plugins.MachineHub].ID})
	for _, d := range devs {
		if !d.Confirmed {
			continue
		}
		out = append(out, model.ResolvedHost{ID: d.ID, Name: d.Name, State: hostState(d), InstanceID: cpu[d.ID].ID})
	}
	return out, nil
}

// hostState 是设备那一格的状态：维护模式优先；未在线且协议不兼容时为 incompatible（需要升级）；
// 其余取在线状态（没有时按 pending）。
func hostState(d model.Device) string {
	switch {
	case d.Maintenance:
		return model.HostStateMaintenance
	case d.Incompatible != nil && d.Presence != model.PresenceOnline:
		return model.HostStateIncompatible
	case d.Presence == "":
		return model.PresencePending
	}
	return d.Presence
}

// resolveHosts 解析主机灯组：Hosts 为各格，Slots["items"] 按灯组顺序放各机器的 cpu 引用
// （使这些实例进入屏幕数据推送）。整块的展示状态固定为 ok：任何一台设备离线都不应让整块灰显，
// 各格自己表达状态。
func (s *Service) resolveHosts(rw *model.ResolvedWidget, lang string, sn *snapshot, broken bool) {
	if broken {
		rw.DisplayState = string(report.DisplayBroken)
		return
	}
	rw.DisplayState = string(report.DisplayOK)
	rw.Hosts = append([]model.ResolvedHost(nil), sn.hosts...)
	refs := make([]model.WidgetRef, 0, len(sn.hosts))
	for _, h := range sn.hosts {
		if h.InstanceID != "" {
			refs = append(refs, model.WidgetRef{InstanceID: h.InstanceID, Item: hostCPUItem})
		}
	}
	if len(refs) > 0 {
		rw.Slots["items"] = s.resolveRefs(refs, lang, sn)
	}
}
