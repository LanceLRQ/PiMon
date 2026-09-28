# PiMon

> 跑在树莓派 + 7 寸小屏（触摸可选）上的插件化监控面板：AI 额度与任务、服务器、NAS、服务、网络一屏看清，网页远程编辑屏幕。

## 项目一句话定义

PiMon 是一个自托管的桌面监控看板：树莓派运行中枢服务（hub），在局域网内收集 AI 订阅额度/API 余额、AI Coding 任务状态、Linux 主机与 NAS 状态、服务可用性和网络连通性，显示在一块 HDMI 小屏上（触摸可选），异常时推送到手机/IM。

- 类比：Android 桌面小组件之于手机，PiMon 之于你的桌面副屏——固定网格、一屏看完、点开看详情。
- 不是 Prometheus/Grafana 那样的通用时序监控平台，也不是公网 SaaS；它面向个人/家庭实验室，重在"瞄一眼就知道是否正常"。

## 核心设计要点

- **中枢 + agent 双向长连接**：局域网 agent 主动连中枢，公网 VPS 由中枢主动连 agent；连接建立后走同一套 WebSocket(TLS) 协议，配置全部在网页统一管理，agent 本地不落盘密钥。
- **一切皆插件**：数据源与通知渠道都是插件，支持三种形态——内置 Go 插件、exec 插件（任意语言可执行文件）、HTTP 插件；插件用 `plugin.yaml` 声明配置表单、产出数据与小组件，不自带前端代码。
- **插件可在中枢或 agent 上运行**：SMART、Docker、Claude 登录态等只能在被监控机器本地读取的数据由 agent 执行插件获取。
- **screen + 固定网格小组件**：`index` 首页 + 任意多个 screen，按网格一屏显示、禁止滚动；小组件尺寸（1x1、2x1、2x2……）由插件声明，前端通用模板渲染。
- **网页远程编辑**：布局保存即生成新版本并实时推送到屏幕热更新，无需远程桌面。
- **缺失不当作零**：采集失败保留上次成功值并标记过期，未知显示为未知。

## 技术栈

- **服务端**：Go（模块根目录 `src/`），`pimon-hub` 与 `pimon-agent` 两个单二进制；SQLite（`modernc.org/sqlite`，CGO_ENABLED=0）；WebSocket over TLS（自签名证书 + 指纹钉扎）。
- **前端**：React + Vite + TypeScript，shadcn/ui + lucide 图标 + Tailwind CSS；图表用 shadcn charts（Recharts）；中英双语；构建产物经 `go:embed` 打包进 `pimon-hub`。
- **其他**：树莓派 4B + Raspberry Pi OS Trixie 64 位（labwc），Chromium kiosk 显示 `/screen`；网页端口 31415、agent 通道 31418；可选 nginx 反代提供 HTTPS；前端 TS 类型由 tygo 从 Go 模型生成。

## 实现现状

- [x] 需求讨论与总体设计（2026-09-28）
- [ ] 阶段 1 骨架：中枢、登录、插件运行时、存储、代理列表、screen 布局编辑器、本机/网络/天气插件、kiosk 部署
- [ ] 阶段 2 agent：双向连接与配对，主机/Docker/systemd/存储健康插件
- [ ] 阶段 3 AI：Hooks 任务状态、各平台额度与 API 余额插件
- [ ] 阶段 4 告警：规则引擎与 Webhook/Telegram/飞书钉钉企微/Bark/Gotify 推送
- [ ] 阶段 5 代理：多节点探测与 xray 服务端监控

## 仓库结构

```
src/                 Go 模块根（go.mod）
  cmd/pimon-hub/     中枢入口
  cmd/pimon-agent/   agent 入口
  internal/hub/      中枢实现（HTTP/WS、调度、存储、告警、webui embed）
  internal/agent/    agent 实现（连接、插件执行、Hooks 中继）
  pkg/plugin/        插件运行时与接口（hub/agent 共用）
  pkg/protocol/      WebSocket 消息定义
  pkg/model/         数据模型（前端 TS 类型由此生成）
  plugins/           内置 Go 插件，一个插件一个目录
web/                 前端（React + Vite），构建输出到 src/internal/hub/webui/dist
deploy/              systemd 单元、kiosk 启动脚本
docs/                面向使用者的文档；docs/superpowers/specs/ 为可公开的定稿设计规格
```

> 以上为规划结构，随阶段 1 落地创建。

## 常用命令

```bash
# 以下命令随阶段 1 的 Makefile 落地后生效
make web            # 构建前端到 src/internal/hub/webui/dist
make build          # 构建 hub（linux/arm64）与 agent（linux/amd64、linux/arm64、darwin/arm64）
make test           # go test ./... + 前端 vitest
cd src && go test ./...
cd web && pnpm dev  # 前端开发服务器
```
