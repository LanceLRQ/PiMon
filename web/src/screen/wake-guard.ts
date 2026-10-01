/**
 * 关屏后唤醒的第一次触摸只负责点亮屏幕，不触发小组件点击（设计 5.5a）。
 * 屏幕由关转开时上膛，消耗一次触摸后解除；一直亮着时不拦截。
 */
export class WakeTouchGuard {
  private mode: 'on' | 'off' | null = null
  private armed = false

  setMode(mode: 'on' | 'off') {
    if (mode === 'on' && this.mode === 'off') this.armed = true
    if (mode === 'off') this.armed = false
    this.mode = mode
  }

  /** 收到一次触摸：返回 true 表示这次要被吞掉 */
  consume(): boolean {
    if (!this.armed) return false
    this.armed = false
    return true
  }
}
