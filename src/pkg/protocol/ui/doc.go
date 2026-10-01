// Package ui 定义浏览器与中枢之间的 UI WebSocket 协议（路径 /ws）。
//
// 连接建立后服务端先发一条 snapshot，之后只发 patch；断线重连一律重新发 snapshot。
// 客户端每 20 秒发一次 ping，服务端超过 60 秒没收到任何客户端消息就断开连接。
// 前端 TS 类型由 tygo 从本包生成。
package ui
