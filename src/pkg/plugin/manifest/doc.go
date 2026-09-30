// Package manifest 解析并校验插件描述文件 plugin.yaml。
//
// 解析基于 yaml.Node，保留声明顺序与行号；所有校验问题一次性收集到 *Error 中，
// 每条 Problem 带 yaml 行号，供 plugin validate 命令打印、API 在
// plugin.invalid_manifest 的 details 里返回。
//
// 约定：
//   - 动态集合在 outputs 里用 "disk[*]" 形式声明（也接受只写前缀 "disk"）；
//     bind 与 alerts 可引用 "disk[/vol1]"、"disk[*]"，前缀必须已声明。
//   - 未写 interval/timeout 时取 DefaultInterval/DefaultTimeout。
//   - 除 alerts 外，出现未知字段一律报错，避免拼写错误被静默忽略；alerts 本期只解析并存储。
package manifest
