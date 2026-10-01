import { describe, expect, it } from 'vitest'
import { WakeTouchGuard } from './wake-guard'

describe('唤醒后首次触摸只点亮不点击（Ruling 33）', () => {
  it('一直亮着时触摸不被吞掉', () => {
    const g = new WakeTouchGuard()
    g.setMode('on')
    expect(g.consume()).toBe(false)
  })

  it('由关屏转为亮屏后，第一次触摸被吞掉，之后的正常', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    g.setMode('on')
    expect(g.consume()).toBe(true)
    expect(g.consume()).toBe(false)
  })

  it('页面一开始就是关屏，随后亮屏同样算唤醒', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    expect(g.consume()).toBe(false)
    g.setMode('on')
    expect(g.consume()).toBe(true)
  })

  it('亮屏后重复收到 on 不重新上膛，关屏期间的触摸不消耗', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    expect(g.consume()).toBe(false)
    g.setMode('on')
    g.setMode('on')
    expect(g.consume()).toBe(true)
    g.setMode('on')
    expect(g.consume()).toBe(false)
  })

  it('再次关屏会解除上膛，下一次亮屏重新上膛', () => {
    const g = new WakeTouchGuard()
    g.setMode('off')
    g.setMode('on')
    g.setMode('off')
    g.setMode('on')
    expect(g.consume()).toBe(true)
  })
})
