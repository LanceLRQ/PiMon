import type { LiveStore } from '@/store/live-store'
import type { ClientMessage, ErrorMessage, Patch, Pong, Snapshot } from '@/types/protocol.generated'

// index.html 里 hub 注入的构建版本占位符；开发模式（Vite）下不会被替换，此时不比较 build
export const buildPlaceholder = '__PIMON_BUILD__'

export function pageBuild(): string | null {
  const v = document.querySelector('meta[name="pimon-build"]')?.getAttribute('content')
  return v && v !== buildPlaceholder ? v : null
}

export function defaultSocketUrl(): string {
  const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${location.host}/ws`
}

export interface LiveSocketOptions {
  store: LiveStore
  url?: string
  // 以下均可注入，便于测试
  createSocket?: (url: string) => WebSocket
  reload?: () => void
  getPageBuild?: () => string | null
  random?: () => number
  // 某次连接在握手阶段就失败（未收到 open）：很可能是会话失效，由外壳重新查询会话
  onHandshakeFailed?: () => void
  pingIntervalMs?: number
  baseDelayMs?: number
  maxDelayMs?: number
}

// 浏览器与中枢之间的 UI WebSocket：连上后服务端先发 snapshot，之后只发 patch；
// 每 20 秒 ping 一次（服务端 60 秒无消息会断开）；断线后指数退避重连。
export class LiveSocket {
  private readonly opts: Required<Omit<LiveSocketOptions, 'onHandshakeFailed'>> & Pick<LiveSocketOptions, 'onHandshakeFailed'>
  private socket: WebSocket | null = null
  private pingTimer: ReturnType<typeof setInterval> | null = null
  private retryTimer: ReturnType<typeof setTimeout> | null = null
  private attempt = 0
  private stopped = true

  constructor(options: LiveSocketOptions) {
    this.opts = {
      url: defaultSocketUrl(),
      createSocket: (u) => new WebSocket(u),
      reload: () => location.reload(),
      getPageBuild: pageBuild,
      random: Math.random,
      pingIntervalMs: 20_000,
      baseDelayMs: 1_000,
      maxDelayMs: 30_000,
      ...options,
    }
  }

  start() {
    if (!this.stopped) return
    this.stopped = false
    this.connect()
  }

  stop() {
    this.stopped = true
    this.clearTimers()
    const s = this.socket
    this.socket = null
    if (s) {
      s.onopen = s.onmessage = s.onclose = s.onerror = null
      s.close()
    }
    this.opts.store.setConnected(false)
  }

  private clearTimers() {
    if (this.pingTimer) clearInterval(this.pingTimer)
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.pingTimer = this.retryTimer = null
  }

  private connect() {
    let opened = false
    const socket = this.opts.createSocket(this.opts.url)
    this.socket = socket
    socket.onopen = () => {
      opened = true
      this.attempt = 0
      this.opts.store.setConnected(true)
      this.pingTimer = setInterval(() => this.send({ type: 'ping' }), this.opts.pingIntervalMs)
    }
    socket.onmessage = (ev) => this.handle(ev.data)
    socket.onclose = () => {
      if (this.socket !== socket) return
      this.socket = null
      this.clearTimers()
      this.opts.store.setConnected(false)
      if (this.stopped) return
      if (!opened) this.opts.onHandshakeFailed?.()
      this.scheduleReconnect()
    }
  }

  private send(msg: ClientMessage) {
    if (this.socket?.readyState === WebSocket.OPEN) this.socket.send(JSON.stringify(msg))
  }

  // 退避：base × 2^次数，封顶 max，再乘 0.5–1 的抖动
  private scheduleReconnect() {
    const { baseDelayMs, maxDelayMs, random } = this.opts
    const delay = Math.min(maxDelayMs, baseDelayMs * 2 ** this.attempt) * (0.5 + random() * 0.5)
    this.attempt++
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null
      if (!this.stopped) this.connect()
    }, delay)
  }

  private handle(data: unknown) {
    let msg: { type?: string }
    try {
      msg = JSON.parse(String(data)) as { type?: string }
    } catch {
      return
    }
    const { store } = this.opts
    switch (msg.type) {
      case 'snapshot': {
        // 中枢升级后 build 与页面注入的版本不一致：整页刷新拿新前端，不再应用旧协议数据
        const snap = msg as Snapshot
        const local = this.opts.getPageBuild()
        if (local && snap.build && snap.build !== local) {
          this.opts.reload()
          return
        }
        store.applySnapshot(snap)
        break
      }
      case 'patch':
        store.applyPatch(msg as Patch)
        break
      case 'pong':
        store.applyServerTime((msg as Pong).server_time)
        break
      case 'error': {
        const { code, details } = (msg as ErrorMessage).error
        store.setError({ code, details: details ?? {} })
        break
      }
      default:
        break
    }
  }
}
