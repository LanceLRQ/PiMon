/**
 * 转亮后多久之内的第一次触摸被视为「点亮屏幕的那一下」。
 * 只用于竞态保护：触摸事件在页面状态刚切到 on 之后才送达。窗口很短，免得吞掉用户有意的点击。
 */
export const WAKE_TOUCH_WINDOW_MS = 1_000

/**
 * 「唤醒那一下」只吞一次（Ruling 51）：
 * - 关屏期间页面收到了触摸：已被 off 分支吞掉，转亮后不再上膛；
 * - 关屏期间页面没收到触摸（HDMI 关闭时收不到，或计划、远程亮屏）：转亮后只上膛一个 1 秒窗口，
 *   防止唤醒触摸的事件在状态切到 on 之后才送达；窗口内的第一次触摸被吞，过后不再吞。
 */
export class WakeTouchGuard {
  private mode: 'on' | 'off' | null = null
  private armedUntil: number | null = null
  private touchedWhileOff = false
  private readonly now: () => number

  constructor(now: () => number = () => Date.now()) {
    this.now = now
  }

  setMode(mode: 'on' | 'off') {
    if (mode === 'on' && this.mode === 'off' && !this.touchedWhileOff) this.armedUntil = this.now() + WAKE_TOUCH_WINDOW_MS
    if (mode === 'off') this.armedUntil = null
    if (mode !== this.mode) this.touchedWhileOff = false
    this.mode = mode
  }

  /** 关屏期间页面吞掉了一次触摸（pointerdown）；亮屏时调用无效 */
  noteTouchWhileOff() {
    if (this.mode === 'off') this.touchedWhileOff = true
  }

  /** 收到一次触摸：返回 true 表示这次要被吞掉 */
  consume(): boolean {
    const until = this.armedUntil
    this.armedUntil = null
    return until !== null && this.now() < until
  }
}
