// Screen Wake Lock：页面持有 screen 锁，防止系统在显示监控面板时息屏（设计 5.5a 防息屏三层之一）。
// 浏览器会在页面隐藏时自动释放锁，所以页面重新可见、锁被释放后都要重新申请；
// 申请失败（权限、省电策略）静默处理并按间隔重试；不支持的浏览器直接跳过。
// 关屏（screen_state off）期间同样保持持有：真正关闭显示器电源由 kiosk 通过显示器电源命令完成，
// 页面在关屏时释放锁没有收益，反而会在唤醒瞬间留下一段没有锁的空窗。

export interface WakeLockSentinelLike {
  readonly released: boolean
  release(): Promise<void>
  addEventListener(type: 'release', listener: () => void): void
}

export interface WakeLockDeps {
  nav?: { wakeLock?: { request(type: 'screen'): Promise<WakeLockSentinelLike> } }
  doc?: Pick<Document, 'visibilityState' | 'addEventListener' | 'removeEventListener'>
  /** 申请失败后的重试间隔，默认 30 秒 */
  retryMs?: number
}

export class WakeLockKeeper {
  private readonly nav: NonNullable<WakeLockDeps['nav']>
  private readonly doc: NonNullable<WakeLockDeps['doc']>
  private readonly retryMs: number
  private sentinel: WakeLockSentinelLike | null = null
  private requesting = false
  private retryTimer: ReturnType<typeof setTimeout> | null = null
  private running = false

  constructor(deps: WakeLockDeps = {}) {
    this.nav = deps.nav ?? (typeof navigator === 'undefined' ? {} : (navigator as unknown as NonNullable<WakeLockDeps['nav']>))
    this.doc = deps.doc ?? document
    this.retryMs = deps.retryMs ?? 30_000
  }

  private readonly onVisibility = () => {
    if (this.doc.visibilityState === 'visible') void this.acquire()
  }

  start() {
    if (this.running || !this.nav.wakeLock) return
    this.running = true
    this.doc.addEventListener('visibilitychange', this.onVisibility)
    void this.acquire()
  }

  stop() {
    if (!this.running) return
    this.running = false
    this.doc.removeEventListener('visibilitychange', this.onVisibility)
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    const s = this.sentinel
    this.sentinel = null
    if (s && !s.released) void s.release().catch(() => {})
  }

  private async acquire() {
    if (!this.running || this.requesting || this.doc.visibilityState !== 'visible') return
    if (this.sentinel && !this.sentinel.released) return
    this.requesting = true
    try {
      const sentinel = await this.nav.wakeLock!.request('screen')
      if (!this.running) {
        void sentinel.release().catch(() => {})
        return
      }
      this.sentinel = sentinel
      sentinel.addEventListener('release', () => {
        if (this.sentinel === sentinel) this.sentinel = null
        // 页面仍可见时被释放（系统策略等）：立刻重新申请；隐藏时等重新可见
        void this.acquire()
      })
    } catch {
      this.scheduleRetry()
    } finally {
      this.requesting = false
    }
  }

  private scheduleRetry() {
    if (!this.running || this.retryTimer) return
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null
      void this.acquire()
    }, this.retryMs)
  }
}
