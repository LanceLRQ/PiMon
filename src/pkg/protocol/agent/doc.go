// Package agent 定义 hub 与 agent 之间 WebSocket 通道上的消息、协议版本、帧大小上限与错误原因码。
//
// 每帧是一个 JSON 文本对象，顶层 "type" 区分消息，其余字段与 type 同层（扁平）。
// 本包不依赖 hub/agent 内部包，也不生成前端 TS 类型（浏览器不使用此协议）。
package agent
