/** 唤醒后多久之内的第一次触摸被视为「点亮屏幕的那一下」；超时视为用户已经隔了一段时间才来操作 */
export const WAKE_TOUCH_WINDOW_MS = 10_000

/**
 * 关屏后唤醒的第一次触摸只负责点亮屏幕，不触发小组件点击（设计 5.5a）。
 * 屏幕由关转开时上膛，10 秒内的第一次触摸被吞掉；超过 10 秒自动解除（例如计划亮屏后几小时才有人点击）。
 */
export class WakeTouchGuard {
  private mode: 'on' | 'off' | null = null
  private armedUntil: number | null = null
  private readonly now: () => number

  constructor(now: () => number = () => Date.now()) {
    this.now = now
  }

  setMode(mode: 'on' | 'off') {
    if (mode === 'on' && this.mode === 'off') this.armedUntil = this.now() + WAKE_TOUCH_WINDOW_MS
    if (mode === 'off') this.armedUntil = null
    this.mode = mode
  }

  /** 收到一次触摸：返回 true 表示这次要被吞掉 */
  consume(): boolean {
    const until = this.armedUntil
    this.armedUntil = null
    return until !== null && this.now() < until
  }
}
