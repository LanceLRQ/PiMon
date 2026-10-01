import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  ALERT_HOLD_MS,
  DETAIL_IDLE_MS,
  HOME_ID,
  ScreenNavigator,
  TOUCH_HOLD_MS,
  type NavConfig,
} from './state-machine'

function config(over: Partial<NavConfig> = {}): NavConfig {
  return {
    screens: [
      { id: 'index', dwellSeconds: 0, inRotation: true, widgetIds: ['w1', 'w2'] },
      { id: 's1', dwellSeconds: 0, inRotation: true, widgetIds: ['w3'] },
      { id: 's2', dwellSeconds: 30, inRotation: true, widgetIds: [] },
      { id: 's3', dwellSeconds: 0, inRotation: false, widgetIds: ['w4'] },
    ],
    carouselMode: 'auto',
    idleHomeSeconds: 60,
    defaultDwellSeconds: 15,
    touch: false,
    ...over,
  }
}

const sec = (n: number) => vi.advanceTimersByTime(n * 1000)

function make(over: Partial<NavConfig> = {}) {
  const nav = new ScreenNavigator()
  nav.configure(config(over))
  return nav
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('自动轮播', () => {
  it('按停留时长依次切换，跳过不参与轮播的，末尾回到首页', () => {
    const nav = make()
    expect(nav.getState().screenId).toBe(HOME_ID)
    sec(14)
    expect(nav.getState().screenId).toBe(HOME_ID)
    sec(1)
    expect(nav.getState().screenId).toBe('s1')
    sec(15)
    expect(nav.getState().screenId).toBe('s2')
    // s2 单独设了 30 秒
    sec(29)
    expect(nav.getState().screenId).toBe('s2')
    sec(1)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })

  it('只有首页参与轮播时原地不动', () => {
    const nav = make({
      screens: [
        { id: 'index', dwellSeconds: 0, inRotation: true, widgetIds: [] },
        { id: 's1', dwellSeconds: 0, inRotation: false, widgetIds: [] },
      ],
    })
    sec(120)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })

  it('只显示首页模式不自动切换', () => {
    const nav = make({ carouselMode: 'home_only' })
    sec(600)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })

  it('用相同配置重复 configure 不重置计时', () => {
    const nav = make()
    sec(10)
    nav.configure(config())
    sec(5)
    expect(nav.getState().screenId).toBe('s1')
    nav.dispose()
  })
})

describe('远程命令', () => {
  it('远程切换立即生效，保持到该屏的停留时长后继续轮播', () => {
    const nav = make()
    expect(nav.remoteSwitch('s3')).toBe(true)
    expect(nav.getState().screenId).toBe('s3')
    // s3 用默认 15 秒；保持期内轮播不动
    sec(14)
    expect(nav.getState().screenId).toBe('s3')
    // 保持期结束后重新计时一个停留时长，再切到下一个参与轮播的（首页）
    sec(1)
    expect(nav.getState().screenId).toBe('s3')
    sec(15)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })

  it('切到不存在的 screen 被忽略', () => {
    const nav = make()
    expect(nav.remoteSwitch('nope')).toBe(false)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })

  it('只显示首页模式下远程切走后空闲到时回首页', () => {
    const nav = make({ carouselMode: 'home_only' })
    nav.remoteSwitch('s1')
    sec(59)
    expect(nav.getState().screenId).toBe('s1')
    sec(1)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })
})

describe('触摸', () => {
  it('没有触摸能力时滑动与打开详情都被忽略', () => {
    const nav = make({ touch: false })
    nav.swipe(1)
    nav.openDetail('w1')
    expect(nav.getState().screenId).toBe(HOME_ID)
    expect(nav.getState().detail).toBeNull()
    nav.dispose()
  })

  it('滑动切换相邻 screen（含不参与轮播的），触摸后暂停轮播 60 秒', () => {
    const nav = make({ touch: true })
    nav.swipe(1)
    expect(nav.getState().screenId).toBe('s1')
    nav.swipe(-1)
    nav.swipe(-1)
    // 首页往左滑回到最后一个 screen
    expect(nav.getState().screenId).toBe('s3')
    sec(59)
    expect(nav.getState().screenId).toBe('s3')
    sec(1)
    // 暂停结束后重新计时一个停留时长
    expect(nav.getState().screenId).toBe('s3')
    sec(15)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })

  it('触摸活动续期暂停时间', () => {
    const nav = make({ touch: true })
    nav.swipe(1)
    sec(50)
    nav.touch()
    sec(50)
    expect(nav.getState().screenId).toBe('s1')
    nav.dispose()
  })

  it('只显示首页模式下触摸后空闲到时回首页', () => {
    const nav = make({ touch: true, carouselMode: 'home_only' })
    nav.swipe(1)
    expect(nav.getState().screenId).toBe('s1')
    sec(40)
    nav.touch()
    sec(59)
    expect(nav.getState().screenId).toBe('s1')
    sec(1)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })
})

describe('优先级：远程命令 > 严重告警 > 触摸 > 轮播', () => {
  it('远程命令能打断触摸的暂停与详情层', () => {
    const nav = make({ touch: true })
    nav.openDetail('w1')
    expect(nav.getState().detail).toEqual({ screenId: HOME_ID, widgetId: 'w1' })
    nav.remoteSwitch('s1')
    expect(nav.getState().screenId).toBe('s1')
    expect(nav.getState().detail).toBeNull()
    nav.dispose()
  })

  it('触摸不能打断远程命令的保持期，保持期过后可以', () => {
    const nav = make({ touch: true })
    nav.remoteSwitch('s1')
    nav.swipe(1)
    nav.openDetail('w3')
    expect(nav.getState().screenId).toBe('s1')
    expect(nav.getState().detail).toBeNull()
    sec(15)
    nav.swipe(1)
    expect(nav.getState().screenId).toBe('s2')
    nav.dispose()
  })

  it('严重告警能打断触摸与详情层并切到目标 screen', () => {
    const nav = make({ touch: true })
    nav.openDetail('w1')
    expect(nav.alert({ screenId: 's1' })).toBe(true)
    expect(nav.getState().screenId).toBe('s1')
    expect(nav.getState().detail).toBeNull()
    nav.dispose()
  })

  it('严重告警不能打断远程命令的保持期，远程命令能打断告警', () => {
    const nav = make({ touch: true })
    nav.remoteSwitch('s1')
    expect(nav.alert({ screenId: 's2' })).toBe(false)
    expect(nav.getState().screenId).toBe('s1')
    sec(15)
    expect(nav.alert({ screenId: 's2' })).toBe(true)
    expect(nav.getState().screenId).toBe('s2')
    // 触摸打断不了告警
    nav.swipe(1)
    expect(nav.getState().screenId).toBe('s2')
    nav.remoteSwitch('s3')
    expect(nav.getState().screenId).toBe('s3')
    nav.dispose()
  })

  it('告警保持期过后轮播恢复', () => {
    const nav = make()
    nav.alert({ screenId: 's1' })
    vi.advanceTimersByTime(ALERT_HOLD_MS)
    expect(nav.getState().screenId).toBe('s1')
    sec(15)
    expect(nav.getState().screenId).toBe('s2')
    nav.dispose()
  })
})

describe('详情层', () => {
  it('打开期间暂停轮播与回首页计时', () => {
    const nav = make({ touch: true })
    nav.openDetail('w1')
    sec(DETAIL_IDLE_MS / 1000 - 1)
    nav.detailActivity()
    sec(DETAIL_IDLE_MS / 1000 - 1)
    expect(nav.getState().detail).not.toBeNull()
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })

  it('空闲 60 秒自动关闭，之后触摸暂停再保持 60 秒', () => {
    const nav = make({ touch: true })
    nav.openDetail('w1')
    sec(59)
    expect(nav.getState().detail).not.toBeNull()
    sec(1)
    expect(nav.getState().detail).toBeNull()
    sec(TOUCH_HOLD_MS / 1000 - 1)
    expect(nav.getState().screenId).toBe(HOME_ID)
    sec(1 + 15)
    expect(nav.getState().screenId).toBe('s1')
    nav.dispose()
  })

  it('只能打开当前 screen 上存在的小组件', () => {
    const nav = make({ touch: true })
    nav.openDetail('w3')
    expect(nav.getState().detail).toBeNull()
    nav.dispose()
  })

  it('主动关闭后回到原 screen', () => {
    const nav = make({ touch: true })
    nav.swipe(1)
    sec(0)
    nav.openDetail('w3')
    nav.closeDetail()
    expect(nav.getState().detail).toBeNull()
    expect(nav.getState().screenId).toBe('s1')
    nav.dispose()
  })
})

describe('布局热更新', () => {
  it('当前 screen 被删除后回到首页', () => {
    const nav = make({ touch: true })
    nav.remoteSwitch('s1')
    const next = config({ touch: true, screens: config().screens.filter((s) => s.id !== 's1') })
    nav.configure(next)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })

  it('正在查看的小组件被删除后关闭详情层并回到首页', () => {
    const nav = make({ touch: true })
    nav.openDetail('w1')
    const next = config({ touch: true })
    next.screens[0] = { ...next.screens[0], widgetIds: ['w2'] }
    nav.configure(next)
    expect(nav.getState().detail).toBeNull()
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.dispose()
  })

  it('详情层里的小组件还在时保持打开', () => {
    const nav = make({ touch: true })
    nav.openDetail('w1')
    nav.configure(config({ touch: true }))
    expect(nav.getState().detail).not.toBeNull()
    nav.dispose()
  })

  it('触摸能力消失时关闭详情层', () => {
    const nav = make({ touch: true })
    nav.openDetail('w1')
    nav.configure(config({ touch: false }))
    expect(nav.getState().detail).toBeNull()
    nav.dispose()
  })

  it('轮播模式切换后重新计时', () => {
    const nav = make({ carouselMode: 'home_only' })
    sec(100)
    nav.configure(config({ carouselMode: 'auto' }))
    sec(15)
    expect(nav.getState().screenId).toBe('s1')
    nav.dispose()
  })
})

describe('暂停（关屏期间）', () => {
  it('暂停后没有计时器，恢复后重新计一个停留时长', () => {
    const nav = make()
    sec(10)
    nav.setActive(false)
    expect(vi.getTimerCount()).toBe(0)
    sec(600)
    expect(nav.getState().screenId).toBe(HOME_ID)
    nav.setActive(true)
    sec(14)
    expect(nav.getState().screenId).toBe(HOME_ID)
    sec(1)
    expect(nav.getState().screenId).toBe('s1')
    nav.dispose()
  })

  it('暂停期间保持期的到期在恢复时处理', () => {
    const nav = make({ touch: true })
    nav.swipe(1)
    nav.setActive(false)
    sec(120)
    nav.setActive(true)
    expect(nav.getState().holdBy).toBeNull()
    sec(15)
    expect(nav.getState().screenId).toBe('s2')
    nav.dispose()
  })
})

describe('订阅', () => {
  it('状态变化通知订阅者，没变化时 getState 引用不变', () => {
    const nav = make()
    const listener = vi.fn()
    nav.subscribe(listener)
    const before = nav.getState()
    nav.configure(config())
    expect(nav.getState()).toBe(before)
    expect(listener).not.toHaveBeenCalled()
    sec(15)
    expect(listener).toHaveBeenCalledTimes(1)
    nav.dispose()
  })

  it('dispose 后不再有计时器', () => {
    const nav = make()
    nav.dispose()
    expect(vi.getTimerCount()).toBe(0)
  })
})
