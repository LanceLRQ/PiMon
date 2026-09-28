# PiMon

> 把 AI 额度、服务器、NAS 和网络状态放到桌面上的一块小屏幕：树莓派 + 7 寸触摸屏的插件化监控面板。

![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue)

> ⚠️ **开发中**：项目处于设计完成、准备实现的阶段，尚无可用发布版本。

## 简介

PiMon 是一个自托管的桌面监控看板。树莓派运行中枢服务，收集你关心的各类状态——AI 订阅额度与 API 余额、AI Coding 任务进度、Linux 服务器与 NAS 的运行情况、网站与容器可用性、网络与代理连通性——显示在一块 HDMI 触摸小屏上，异常时推送到手机或 IM。

它面向个人和家庭实验室：不是 Prometheus/Grafana 那样的通用时序平台，而是"瞄一眼就知道一切是否正常"的桌面副屏。

## 特性（规划）

- **一屏看清**：固定网格布局、禁止滚动，像手机桌面小组件一样摆放 1x1、2x1、2x2 等尺寸的卡片；首页自带时钟与天气。
- **网页远程编辑**：在手机或电脑浏览器里拖拽编辑屏幕内容，保存后屏幕实时更新，无需远程桌面。
- **多屏轮播与触摸**：首页 + 任意多个 screen，可自动轮播或只显示首页、触摸切换；点卡片查看详情与历史曲线。
- **一切皆插件**：AI 额度（Codex、Claude、GLM、Qoder、各类 API 余额）、主机资源、SMART 与 RAID/Btrfs 存储健康、Docker、systemd、HTTP/TCP/Ping 探测、代理与多节点连通性都以插件提供；支持内置 Go 插件、任意语言的 exec 插件和 HTTP 插件。
- **轻量 agent**：被监控机器运行单文件 Go agent；局域网设备主动连接中枢，公网 VPS 由中枢主动连接，全程 TLS 加密并校验证书指纹。
- **告警推送**：阈值、状态与事件规则，支持通用 Webhook、Telegram、飞书/钉钉/企业微信、Bark、Gotify；免打扰时段与临时静音。

## 硬件与运行环境

- 树莓派 4B（Raspberry Pi OS Bookworm 64 位）
- 7 寸左右 HDMI 显示器（支持 USB 触摸更佳）
- 被监控端：Linux（amd64/arm64）或 macOS（Apple Silicon）

## 安装

尚未发布。首个可用版本（中枢 + 屏幕 + 本机与网络监控）完成后，将在此提供安装步骤。

## 使用

尚未发布。

## 贡献

欢迎提 Issue 讨论需求与问题；提交 PR 前建议先开 Issue 沟通方案。

## License

AGPL-3.0 — 详见 [LICENSE](./LICENSE)。
