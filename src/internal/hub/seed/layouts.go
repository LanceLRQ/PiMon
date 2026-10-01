package seed

import "github.com/LanceLRQ/PiMon/src/pkg/model"

// placement 是种子布局里的一个小组件放置（坐标从 0 起）。聚合小组件用 template，其余用 plugin 与 widget。
type placement struct {
	id        string
	plugin    string
	widget    string
	aggregate bool
	template  string
	w, h      int
	col, row  int
}

func plugin(id, pluginID, widget string, w, h, col, row int) placement {
	return placement{id: id, plugin: pluginID, widget: widget, w: w, h: h, col: col, row: row}
}

func reach(w, h, col, row int) placement {
	return placement{id: "reach", aggregate: true, template: "status-grid", w: w, h: h, col: col, row: row}
}

// placements 是三份种子布局：时钟、天气、树莓派主机指标与网络连通总览。
// 所用尺寸都是 manifest 或通用、聚合目录已声明的。
var placements = map[model.Grid][]placement{
	{Cols: 6, Rows: 4}: {
		plugin("clock", pluginCore, "clock", 4, 2, 0, 0),
		plugin("weather", pluginWeather, "weather", 2, 2, 4, 0),
		plugin("overview", pluginHost, "overview", 2, 2, 0, 2),
		reach(2, 2, 2, 2),
		plugin("cpu", pluginHost, "cpu", 1, 1, 4, 2),
		plugin("memory", pluginHost, "memory", 1, 1, 5, 2),
		plugin("temperature", pluginHost, "temperature", 1, 1, 4, 3),
	},
	{Cols: 8, Rows: 5}: {
		plugin("clock", pluginCore, "clock", 4, 2, 0, 0),
		plugin("weather", pluginWeather, "weather", 4, 2, 4, 0),
		plugin("overview", pluginHost, "overview", 2, 2, 0, 2),
		plugin("disks", pluginHost, "disks", 2, 2, 2, 2),
		reach(4, 2, 4, 2),
		plugin("cpu", pluginHost, "cpu", 1, 1, 0, 4),
		plugin("memory", pluginHost, "memory", 1, 1, 1, 4),
		plugin("temperature", pluginHost, "temperature", 1, 1, 2, 4),
		plugin("network", pluginHost, "network", 2, 1, 3, 4),
	},
	{Cols: 10, Rows: 6}: {
		plugin("clock", pluginCore, "clock", 4, 2, 0, 0),
		plugin("weather", pluginWeather, "weather", 4, 2, 4, 0),
		plugin("network", pluginHost, "network", 2, 1, 8, 0),
		plugin("cpu", pluginHost, "cpu", 1, 1, 8, 1),
		plugin("memory", pluginHost, "memory", 1, 1, 9, 1),
		plugin("overview", pluginHost, "overview", 2, 2, 0, 2),
		plugin("disks", pluginHost, "disks", 2, 2, 2, 2),
		reach(6, 2, 4, 2),
		plugin("temperature", pluginHost, "temperature", 1, 1, 0, 4),
	},
}
