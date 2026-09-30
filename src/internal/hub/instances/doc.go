// Package instances 是插件实例服务：实例仓库（密钥加密入库、保留原值语义）、
// 当前状态（内存为准，每 30 秒落盘，重启恢复）、展示状态计算，
// 以及与插件注册表、调度器、Streamer 管理器和代理仓库的接线。
//
// 数值历史由另一个包负责，本包只在每次成功采集后把报告交给 HistoryRecorder。
package instances
