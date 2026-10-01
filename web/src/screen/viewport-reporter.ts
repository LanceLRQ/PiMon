import type { ClientMessage } from '@/types/protocol.generated'

// 屏幕向中枢上报视口与当前 screen。服务端自己有稳定性判定（连续稳定 2 秒才采信，关屏与唤醒后 5 秒内忽略），
// 被忽略的上报不会重试，所以前端在两处补报：连接建立时、由关屏转亮屏 6 秒后（Ruling 37）。

export interface ViewportReading {
  w: number
  h: number
  dpr: number
}

export interface ViewportReporterDeps {
  send: (msg: ClientMessage) => void
  read: () => ViewportReading
  /** 是否有触摸（any-pointer: coarse） */
  coarse: () => boolean
  /** 订阅窗口尺寸变化，返回取消函数 */
  onResize: (fn: () => void) => () => void
  /** 尺寸变化防抖时长 */
  debounceMs?: number
  /** 亮屏后补报延迟 */
  wakeReportMs?: number
}

export function readWindowViewport(): ViewportReading {
  return { w: window.innerWidth, h: window.innerHeight, dpr: window.devicePixelRatio || 1 }
}

export function windowCoarsePointer(): boolean {
  return typeof matchMedia === 'function' && matchMedia('(any-pointer: coarse)').matches
}

export class ViewportReporter {
  private off: (() => void) | null = null
  private debounce: ReturnType<typeof setTimeout> | null = null
  private wake: ReturnType<typeof setTimeout> | null = null
  private mode: 'on' | 'off' | null = null
  private currentScreen: string | null = null
  private readonly debounceMs: number
  private readonly wakeReportMs: number
  private readonly deps: ViewportReporterDeps

  constructor(deps: ViewportReporterDeps) {
    this.deps = deps
    this.debounceMs = deps.debounceMs ?? 1000
    this.wakeReportMs = deps.wakeReportMs ?? 6000
  }

  start() {
    if (this.off) return
    this.off = this.deps.onResize(() => this.scheduleResize())
  }

  stop() {
    this.off?.()
    this.off = null
    this.clearDebounce()
    this.clearWake()
  }

  /** 连接建立：立即上报视口、触摸能力与当前 screen */
  reportNow() {
    this.send(true)
  }

  /** 屏幕开关状态：由关转开 6 秒后补报，关屏期间的尺寸变化不上报 */
  onScreenMode(mode: 'on' | 'off') {
    const prev = this.mode
    this.mode = mode
    this.clearWake()
    if (mode === 'off') {
      this.clearDebounce()
      return
    }
    if (prev === 'off') {
      this.wake = setTimeout(() => {
        this.wake = null
        this.send(false)
      }, this.wakeReportMs)
    }
  }

  /** 当前显示的 screen 变化时上报，相同的不重复 */
  reportCurrentScreen(id: string) {
    if (id === this.currentScreen) return
    this.currentScreen = id
    this.deps.send({ type: 'viewport_report', current_screen: id })
  }

  private scheduleResize() {
    if (this.mode === 'off') return
    this.clearDebounce()
    this.debounce = setTimeout(() => {
      this.debounce = null
      this.send(false)
    }, this.debounceMs)
  }

  private send(withScreen: boolean) {
    const v = this.deps.read()
    const msg: ClientMessage = {
      type: 'viewport_report',
      viewport: { w: v.w, h: v.h, dpr: v.dpr },
      coarse_pointer: this.deps.coarse(),
    }
    if (withScreen && this.currentScreen) msg.current_screen = this.currentScreen
    this.deps.send(msg)
  }

  private clearDebounce() {
    if (this.debounce) clearTimeout(this.debounce)
    this.debounce = null
  }

  private clearWake() {
    if (this.wake) clearTimeout(this.wake)
    this.wake = null
  }
}
