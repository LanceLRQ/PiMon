// Package plugindev 提供 exec 插件开发者工具：plugin validate 校验插件目录，
// plugin run 在本机运行一次并打印报告摘要，另外负责生成 docs/plugin-schema/ 下的 JSON Schema。
// 开发工具不做属主与权限的安全检查（那是 hub 加载插件时的约束）。
package plugindev
