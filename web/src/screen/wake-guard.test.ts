import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { WAKE_TOUCH_WINDOW_MS, WakeTouchGuard } from './wake-guard'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('唤醒那一下只吞一次（Ruling 51）', () => {
  it('窗口只有 1 秒', () => {
    expect(WAKE_TOUCH_WINDOW_MS).toBe(1000)
  })

  it('一直亮着时触摸不被吞掉', () => {
    const g = new WakeTouchGuard()
    g.setMode('on')
    expect(g.consume()).toBe(false)
  })

  it('关屏期间页面吞过触摸，唤醒后首触直接放行', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    g.noteTouchWhileOff()
    g.setMode('on')
    expect(g.consume()).toBe(false)
  })

  it('关屏期间无触摸，转亮后 1 秒内的第一次触摸被吞，第二次放行', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    g.setMode('on')
    vi.advanceTimersByTime(WAKE_TOUCH_WINDOW_MS - 1)
    expect(g.consume()).toBe(true)
    expect(g.consume()).toBe(false)
  })

  it('关屏期间无触摸，转亮满 1 秒后的首触放行', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    g.setMode('on')
    vi.advanceTimersByTime(WAKE_TOUCH_WINDOW_MS)
    expect(g.consume()).toBe(false)
  })

  it('页面一开始就是关屏，随后无触摸亮屏同样走 1 秒窗口', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    g.setMode('on')
    expect(g.consume()).toBe(true)
  })

  it('亮屏时的 noteTouchWhileOff 无效，不影响之后的唤醒窗口', () => {
    const g = new WakeTouchGuard()
    g.setMode('on')
    g.noteTouchWhileOff()
    g.setMode('off')
    g.setMode('on')
    expect(g.consume()).toBe(true)
  })

  it('上一轮关屏的触摸不会带到下一轮关屏', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    g.noteTouchWhileOff()
    g.setMode('on')
    expect(g.consume()).toBe(false)
    g.setMode('off')
    g.setMode('on')
    expect(g.consume()).toBe(true)
  })

  it('重复收到 on 不重新上膛，再次关屏会解除上膛', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    g.setMode('on')
    g.setMode('on')
    expect(g.consume()).toBe(true)
    g.setMode('on')
    expect(g.consume()).toBe(false)
    g.setMode('off')
    g.setMode('on')
    g.setMode('off')
    expect(g.consume()).toBe(false)
  })
})
