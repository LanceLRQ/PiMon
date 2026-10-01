import { dwellMs, nextInRotation, type RotationScreen } from './carousel'

// 屏幕状态机（设计 5.4）。优先级从高到低：远程命令 > 严重告警 > 用户触摸 > 轮播。
// 规则：某个来源发出事件后会「保持」当前 screen 一段时间；之后到达的事件只有优先级不低于当前保持者才生效，
// 轮播是最低优先级，保持期内不推进。详情层打开期间轮播与回首页计时都暂停。
// 严重告警由 M4 的告警引擎接入，这里只提供 alert() 入口。

export const HOME_ID = 'index'
/** 用户触摸后暂停轮播的时长 */
export const TOUCH_HOLD_MS = 60_000
/** 详情层无操作自动关闭的时长 */
export const DETAIL_IDLE_MS = 60_000
/** 严重告警跳转后保持当前 screen 的时长 */
export const ALERT_HOLD_MS = 60_000

export interface NavScreen extends RotationScreen {
  widgetIds: string[]
}

export interface NavConfig {
  screens: NavScreen[]
  carouselMode: 'home_only' | 'auto'
  /** 只显示首页模式下，无操作多少秒回首页 */
  idleHomeSeconds: number
  defaultDwellSeconds: number
  /** 有触摸：才响应滑动、点开详情 */
  touch: boolean
}

export interface DetailTarget {
  screenId: string
  widgetId: string
}

export type HoldSource = 'touch' | 'alert' | 'remote'

export interface NavState {
  screenId: string
  detail: DetailTarget | null
  /** 当前保持 screen 的来源；没有保持时为 null */
  holdBy: HoldSource | null
}

export interface NavDeps {
  now: () => number
  setTimer: (fn: () => void, ms: number) => unknown
  clearTimer: (handle: unknown) => void
}

const defaultDeps: NavDeps = {
  now: () => Date.now(),
  setTimer: (fn, ms) => setTimeout(fn, ms),
  clearTimer: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
}

const rank: Record<HoldSource, number> = { touch: 1, alert: 2, remote: 3 }

const emptyConfig: NavConfig = {
  screens: [],
  carouselMode: 'auto',
  idleHomeSeconds: 60,
  defaultDwellSeconds: 15,
  touch: false,
}

export class ScreenNavigator {
  private cfg: NavConfig = emptyConfig
  private state: NavState = { screenId: HOME_ID, detail: null, holdBy: null }
  // 各类到期时刻（毫秒时间戳），null 表示当前不需要
  private holdUntil: number | null = null
  private carouselAt: number | null = null
  private idleAt: number | null = null
  private detailIdleAt: number | null = null
  private timer: unknown = null
  private configured = false
  private active = true
  private readonly listeners = new Set<() => void>()

  private readonly deps: NavDeps

  constructor(deps: NavDeps = defaultDeps) {
    this.deps = deps
  }

  getState = (): NavState => this.state

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  /** 停止计时；订阅关系由订阅者自己取消（React 严格模式会重复挂载） */
  dispose() {
    if (this.timer !== null) this.deps.clearTimer(this.timer)
    this.timer = null
  }

  /**
   * 暂停/恢复计时（关屏期间暂停，不推进轮播）。恢复时重新计停留与空闲时间，已到期的保持期在此处理。
   */
  setActive(active: boolean) {
    if (this.active === active) return
    this.active = active
    if (!active) {
      if (this.timer !== null) this.deps.clearTimer(this.timer)
      this.timer = null
      return
    }
    if (this.carouselAt !== null) this.rearmCarousel()
    if (this.idleAt !== null) this.armIdle()
    this.tick()
  }

  /** 应用布局与设置；可重复调用，配置没有实质变化时不重置计时 */
  configure(cfg: NavConfig) {
    const prev = this.cfg
    this.cfg = cfg
    const first = !this.configured
    this.configured = true

    const current = cfg.screens.find((s) => s.id === this.state.screenId)
    const detail = this.state.detail
    const detailGone = detail !== null && (!cfg.touch || !current?.widgetIds.includes(detail.widgetId))
    if (!current || detailGone) {
      // 当前 screen 或正在查看的小组件被删除、触摸能力消失：回到首页
      this.clearHold()
      this.detailIdleAt = null
      this.setState({ screenId: this.homeId(), detail: null, holdBy: null })
      this.rearmCarousel()
      this.armIdle()
    } else if (first || prev.carouselMode !== cfg.carouselMode) {
      this.rearmCarousel()
      this.armIdle()
    } else if (this.carouselAt === null) {
      this.rearmCarousel()
    }
    this.reschedule()
  }

  /** 远程命令：最高优先级，切到指定 screen。screen 不存在返回 false */
  remoteSwitch(screenId: string): boolean {
    const target = this.cfg.screens.find((s) => s.id === screenId)
    if (!target || !this.canApply('remote')) return false
    this.closeDetailQuiet()
    this.setState({ screenId })
    this.applyHold('remote', dwellMs(target, this.cfg.defaultDwellSeconds))
    return true
  }

  /** 严重告警（M4 接入）：打断触摸与详情层；target.screenId 给出时跳过去 */
  alert(target: { screenId?: string } = {}): boolean {
    if (!this.canApply('alert')) return false
    this.closeDetailQuiet()
    if (target.screenId && this.cfg.screens.some((s) => s.id === target.screenId)) {
      this.setState({ screenId: target.screenId })
    }
    this.applyHold('alert', ALERT_HOLD_MS)
    return true
  }

  /** 用户滑动：dir 为 1 切到下一个 screen，-1 切到上一个（全部 screen 循环，不限于参与轮播的） */
  swipe(dir: 1 | -1) {
    if (!this.cfg.touch || this.state.detail || !this.canApply('touch')) return
    const n = this.cfg.screens.length
    const at = this.cfg.screens.findIndex((s) => s.id === this.state.screenId)
    if (n < 2 || at < 0) {
      this.applyHold('touch', TOUCH_HOLD_MS)
      return
    }
    this.setState({ screenId: this.cfg.screens[(at + dir + n) % n].id })
    this.applyHold('touch', TOUCH_HOLD_MS)
  }

  /** 任意触摸活动：续期暂停轮播与回首页计时 */
  touch() {
    if (!this.cfg.touch || !this.canApply('touch')) return
    if (this.state.detail) {
      this.detailActivity()
      return
    }
    this.applyHold('touch', TOUCH_HOLD_MS)
  }

  /** 点开当前 screen 上某个小组件的详情层 */
  openDetail(widgetId: string) {
    if (!this.cfg.touch || this.state.detail || !this.canApply('touch')) return
    const current = this.cfg.screens.find((s) => s.id === this.state.screenId)
    if (!current?.widgetIds.includes(widgetId)) return
    this.holdUntil = null
    this.carouselAt = null
    this.idleAt = null
    this.detailIdleAt = this.deps.now() + DETAIL_IDLE_MS
    this.setState({ detail: { screenId: current.id, widgetId }, holdBy: 'touch' })
    this.reschedule()
  }

  /** 详情层里的操作（滑动、点击）：重新计 60 秒空闲 */
  detailActivity() {
    if (!this.state.detail) return
    this.detailIdleAt = this.deps.now() + DETAIL_IDLE_MS
    this.reschedule()
  }

  /** 关闭详情层，回到原 screen；关闭本身算一次触摸 */
  closeDetail() {
    if (!this.state.detail) return
    this.closeDetailQuiet()
    this.applyHold('touch', TOUCH_HOLD_MS)
  }

  // ---- 内部 ----

  private homeId(): string {
    const s = this.cfg.screens
    return s.some((x) => x.id === HOME_ID) ? HOME_ID : (s[0]?.id ?? HOME_ID)
  }

  private setState(patch: Partial<NavState>) {
    const next = { ...this.state, ...patch }
    if (next.screenId === this.state.screenId && next.detail === this.state.detail && next.holdBy === this.state.holdBy) return
    this.state = next
    this.listeners.forEach((l) => l())
  }

  private canApply(source: HoldSource): boolean {
    const holder = this.state.holdBy
    if (!holder) return true
    if (this.holdUntil !== null && this.deps.now() >= this.holdUntil) return true
    return rank[source] >= rank[holder]
  }

  private clearHold() {
    this.holdUntil = null
  }

  private closeDetailQuiet() {
    this.detailIdleAt = null
    if (this.state.detail) this.setState({ detail: null })
  }

  private applyHold(source: HoldSource, ms: number) {
    this.holdUntil = this.deps.now() + ms
    this.carouselAt = null
    this.setState({ holdBy: source })
    this.armIdle()
    this.reschedule()
  }

  private currentScreen(): NavScreen | undefined {
    return this.cfg.screens.find((s) => s.id === this.state.screenId)
  }

  private rearmCarousel() {
    const cur = this.currentScreen()
    if (this.cfg.carouselMode === 'auto' && cur && !this.state.detail && !this.state.holdBy) {
      this.carouselAt = this.deps.now() + dwellMs(cur, this.cfg.defaultDwellSeconds)
    } else {
      this.carouselAt = null
    }
  }

  // 只显示首页模式：离开首页后无操作 idleHomeSeconds 回首页；有保持期时等保持期结束
  private armIdle() {
    if (this.cfg.carouselMode === 'home_only' && this.state.screenId !== this.homeId() && !this.state.detail) {
      this.idleAt = Math.max(this.deps.now() + this.cfg.idleHomeSeconds * 1000, this.holdUntil ?? 0)
    } else {
      this.idleAt = null
    }
  }

  private reschedule() {
    if (this.timer !== null) this.deps.clearTimer(this.timer)
    this.timer = null
    if (!this.active) return
    const due = [this.detailIdleAt, this.holdUntil, this.carouselAt, this.idleAt].filter((t): t is number => t !== null)
    if (due.length === 0) return
    const delay = Math.max(0, Math.min(...due) - this.deps.now())
    this.timer = this.deps.setTimer(() => this.tick(), delay)
  }

  private tick() {
    this.timer = null
    const now = this.deps.now()
    if (this.detailIdleAt !== null && now >= this.detailIdleAt) {
      this.closeDetailQuiet()
      this.applyHold('touch', TOUCH_HOLD_MS)
    }
    if (this.holdUntil !== null && now >= this.holdUntil) {
      this.holdUntil = null
      this.setState({ holdBy: null })
      this.rearmCarousel()
    }
    if (this.carouselAt !== null && now >= this.carouselAt) {
      const next = nextInRotation(this.cfg.screens, this.state.screenId)
      if (next) this.setState({ screenId: next })
      this.rearmCarousel()
    }
    if (this.idleAt !== null && now >= this.idleAt) {
      this.idleAt = null
      this.setState({ screenId: this.homeId() })
      this.rearmCarousel()
    }
    this.reschedule()
  }
}
