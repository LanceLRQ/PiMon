import { act, fireEvent, screen as dom } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Patch } from '@/types/protocol.generated'
import { screenThemeStorageKey } from './theme-apply'
import {
  defaultScreens,
  fakeReporter,
  humidity,
  layoutOf,
  makeData,
  makeWidget,
  renderScreen,
  screenOf,
  setViewport,
  settingsOf,
  storeWith,
  temp,
  textWidget,
} from './test-utils'

const touchSettings = () => settingsOf({ input_mode: 'touch' })

const patchOf = (entity: string, over: Partial<Patch>): Patch => ({
  type: 'patch',
  server_time: new Date().toISOString(),
  entity,
  ...over,
})

const widgetEl = (container: HTMLElement, id: string) => container.querySelector<HTMLElement>(`[data-widget-id="${id}"]`)!

// 一次完整点击：pointerdown → pointerup → click，与真实触摸屏的事件序列一致
function tap(el: Element, x = 100, y = 100) {
  fireEvent.pointerDown(el, { clientX: x, clientY: y, pointerId: 1, pointerType: 'touch' })
  fireEvent.pointerUp(el, { clientX: x, clientY: y, pointerId: 1, pointerType: 'touch' })
  fireEvent.click(el, { clientX: x, clientY: y })
}

function swipe(el: Element, fromX: number, toX: number) {
  fireEvent.pointerDown(el, { clientX: fromX, clientY: 300, pointerId: 1, pointerType: 'touch' })
  fireEvent.pointerUp(el, { clientX: toX, clientY: 305, pointerId: 1, pointerType: 'touch' })
}

beforeEach(() => {
  vi.useFakeTimers()
  setViewport(1024, 600)
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
  document.documentElement.removeAttribute('data-reduce-effects')
})
afterEach(() => {
  vi.useRealTimers()
  localStorage.clear()
})

describe('屏幕根：渲染与网格', () => {
  it('渲染当前 screen（首页）的小组件', async () => {
    const { container } = await renderScreen(storeWith())
    expect(widgetEl(container, 'w1')).not.toBeNull()
    expect(widgetEl(container, 'w2')).not.toBeNull()
    expect(widgetEl(container, 'w3')).toBeNull()
  })

  it('按 viewport 与列行数计算单元格，窗口变化后重算', async () => {
    const { container } = await renderScreen(storeWith())
    expect(widgetEl(container, 'w1').style.width).toBe('256px')
    act(() => {
      setViewport(1280, 720)
      window.dispatchEvent(new Event('resize'))
    })
    expect(widgetEl(container, 'w1').style.width).toBe('320px')
    expect(widgetEl(container, 'w1').style.height).toBe('144px')
  })

  it('没有收到布局时只渲染空底板，不报错', async () => {
    const { container } = await renderScreen(storeWith({ resolved_layout: undefined }))
    expect(container.querySelector('[data-screen-root]')).not.toBeNull()
    expect(container.querySelectorAll('[data-widget-id]')).toHaveLength(0)
  })
})

describe('屏幕根：主题应用', () => {
  it('根元素设置 data-theme，降低特效时同一元素设置 data-reduce-effects，并写回 localStorage', async () => {
    await renderScreen(storeWith({ screen_state: { mode: 'on', theme_id: 'mission-control', reason: 'schedule' }, screen_settings: settingsOf({}, { reduce_effects: true }) }))
    const root = document.documentElement
    expect(root.getAttribute('data-theme')).toBe('mission-control')
    expect(root.hasAttribute('data-reduce-effects')).toBe(true)
    expect(localStorage.getItem(screenThemeStorageKey)).toBe('mission-control')
    expect(root.classList.contains('dark')).toBe(false)
  })

  it('收到 screen_state 与 settings patch 后无刷新切换主题与特效', async () => {
    const store = storeWith()
    const { container } = await renderScreen(store)
    expect(document.documentElement.getAttribute('data-theme')).toBe('ambient')
    const before = widgetEl(container, 'w1')
    act(() => store.applyPatch(patchOf('screen_state', { screen_state: { mode: 'on', theme_id: 'industrial', reason: 'schedule' } })))
    expect(document.documentElement.getAttribute('data-theme')).toBe('industrial')
    expect(localStorage.getItem(screenThemeStorageKey)).toBe('industrial')
    act(() => store.applyPatch(patchOf('settings', { screen_settings: settingsOf({}, { reduce_effects: true }) })))
    expect(document.documentElement.hasAttribute('data-reduce-effects')).toBe(true)
    // 同一个 DOM 节点：没有重新挂载
    expect(widgetEl(container, 'w1')).toBe(before)
    // 屏幕根上的主题根与文档根一致
    expect(container.querySelector('[data-theme="industrial"][data-reduce-effects]')).not.toBeNull()
  })
})

describe('屏幕根：关屏', () => {
  it('关屏时显示纯黑遮罩并卸载小组件，停止渲染动效；亮屏后恢复', async () => {
    const screens = [screenOf('index', [makeWidget({ id: 'c', template: 'clock', source: 'generic', size: { cols: 2, rows: 1 }, title: '时钟' })])]
    const store = storeWith({ resolved_layout: layoutOf(screens) })
    const { container } = await renderScreen(store)
    expect(widgetEl(container, 'c')).not.toBeNull()
    act(() => store.applyPatch(patchOf('screen_state', { screen_state: { mode: 'off', theme_id: 'ambient', reason: 'schedule' } })))
    const overlay = container.querySelector<HTMLElement>('[data-screen-off]')!
    expect(overlay).not.toBeNull()
    expect(overlay.style.backgroundColor).toBe('black')
    expect(container.querySelectorAll('[data-widget-id]')).toHaveLength(0)
    // 时钟每秒重渲的计时器不再运行
    expect(vi.getTimerCount()).toBe(0)
    act(() => store.applyPatch(patchOf('screen_state', { screen_state: { mode: 'on', theme_id: 'ambient', reason: 'schedule' } })))
    expect(container.querySelector('[data-screen-off]')).toBeNull()
    expect(widgetEl(container, 'c')).not.toBeNull()
  })

  it('开关屏状态通知上报器（用于唤醒后补报 viewport）', async () => {
    const store = storeWith()
    const { reporter, calls } = fakeReporter()
    await renderScreen(store, { reporter })
    expect(calls).toContain('mode:on')
    act(() => store.applyPatch(patchOf('screen_state', { screen_state: { mode: 'off', theme_id: 'ambient', reason: 'remote_off' } })))
    act(() => store.applyPatch(patchOf('screen_state', { screen_state: { mode: 'on', theme_id: 'ambient', reason: 'remote_on' } })))
    expect(calls.filter((c) => c.startsWith('mode:'))).toEqual(['mode:on', 'mode:off', 'mode:on'])
  })
})

describe('屏幕根：轮播与 current_screen 上报', () => {
  it('自动轮播按默认停留秒数切换，并上报 current_screen', async () => {
    const { reporter, calls } = fakeReporter()
    const { container } = await renderScreen(storeWith(), { reporter })
    expect(calls).toContain('screen:index')
    act(() => void vi.advanceTimersByTime(15_000))
    expect(widgetEl(container, 'w3')).not.toBeNull()
    expect(widgetEl(container, 'w1')).toBeNull()
    expect(calls).toContain('screen:s1')
  })

  it('每屏停留来自布局，0 用设置里的默认值', async () => {
    const screens = [screenOf('index', [textWidget('w1', 0, 0)], { dwell_seconds: 5 }), screenOf('s1', [textWidget('w3', 0, 0)])]
    const { container } = await renderScreen(storeWith({ resolved_layout: layoutOf(screens) }))
    act(() => void vi.advanceTimersByTime(5000))
    expect(widgetEl(container, 'w3')).not.toBeNull()
  })

  it('只显示首页模式不轮播', async () => {
    const { container } = await renderScreen(storeWith({ screen_settings: settingsOf({ carousel_mode: 'home_only' }) }))
    act(() => void vi.advanceTimersByTime(120_000))
    expect(widgetEl(container, 'w1')).not.toBeNull()
  })

  it('当前 screen 被布局热更新删除后回到首页', async () => {
    const store = storeWith()
    const { container, nav } = await renderScreen(store)
    act(() => void nav.remoteSwitch('s1'))
    expect(widgetEl(container, 'w3')).not.toBeNull()
    act(() => store.applyPatch(patchOf('layout', { resolved_layout: layoutOf([defaultScreens()[0]], 2) })))
    expect(widgetEl(container, 'w1')).not.toBeNull()
    expect(nav.getState().screenId).toBe('index')
  })

  it('远程切换指令走状态机', async () => {
    const { container, nav } = await renderScreen(storeWith())
    act(() => void nav.remoteSwitch('s1'))
    expect(widgetEl(container, 'w3')).not.toBeNull()
  })
})

describe('屏幕根：触摸与详情层', () => {
  const touchStore = () => storeWith({ screen_settings: touchSettings() })

  it('没有触摸时点击小组件不打开详情层', async () => {
    const { container } = await renderScreen(storeWith())
    tap(widgetEl(container, 'w2'))
    expect(container.querySelector('[data-detail-layer]')).toBeNull()
  })

  it('点击小组件打开详情层：标题、该实例全部数据项（用服务端标题）', async () => {
    const { container } = await renderScreen(touchStore())
    tap(widgetEl(container, 'w2'))
    const layer = container.querySelector('[data-detail-layer]')!
    expect(layer).not.toBeNull()
    expect(layer.textContent).toContain('读数w2')
    // temp 在布局里带服务端标题「温度」，humidity 没有标题回落到键
    expect(layer.textContent).toContain('温度')
    expect(layer.textContent).toContain('21.5')
    expect(layer.textContent).toContain('humidity')
    expect(layer.textContent).toContain('60')
  })

  it('详情层不显示滚动条且允许手指竖向滑动', async () => {
    const { container } = await renderScreen(touchStore())
    tap(widgetEl(container, 'w2'))
    const scroller = container.querySelector<HTMLElement>('[data-detail-scroll]')!
    expect(scroller.className).toContain('overflow-y-auto')
    expect(scroller.className).toContain('screen-no-scrollbar')
    expect(scroller.style.touchAction).toBe('pan-y')
  })

  it('点关闭按钮回到原 screen', async () => {
    const { container } = await renderScreen(touchStore())
    tap(widgetEl(container, 'w2'))
    fireEvent.click(dom.getByRole('button', { name: '关闭' }))
    expect(container.querySelector('[data-detail-layer]')).toBeNull()
    expect(widgetEl(container, 'w2')).not.toBeNull()
  })

  it('空闲 60 秒自动关闭，打开期间轮播暂停', async () => {
    const { container, nav } = await renderScreen(touchStore())
    tap(widgetEl(container, 'w2'))
    act(() => void vi.advanceTimersByTime(59_000))
    expect(container.querySelector('[data-detail-layer]')).not.toBeNull()
    expect(nav.getState().screenId).toBe('index')
    act(() => void vi.advanceTimersByTime(1000))
    expect(container.querySelector('[data-detail-layer]')).toBeNull()
  })

  it('详情层里的滚动与点击续期空闲计时', async () => {
    const { container } = await renderScreen(touchStore())
    tap(widgetEl(container, 'w2'))
    act(() => void vi.advanceTimersByTime(50_000))
    fireEvent.scroll(container.querySelector('[data-detail-scroll]')!)
    act(() => void vi.advanceTimersByTime(50_000))
    expect(container.querySelector('[data-detail-layer]')).not.toBeNull()
    act(() => void vi.advanceTimersByTime(10_000))
    expect(container.querySelector('[data-detail-layer]')).toBeNull()
  })

  it('天气小组件的详情层显示 Open-Meteo 署名', async () => {
    const weather = makeWidget({
      id: 'wx',
      template: 'weather',
      source: 'plugin',
      plugin_id: 'weather',
      size: { cols: 2, rows: 1 },
      title: '天气',
      instance_id: 'i1',
      slots: { value: [{ instance_id: 'i1', item: 'temp' }] },
    })
    const store = storeWith({ resolved_layout: layoutOf([screenOf('index', [weather])]), screen_settings: touchSettings() })
    const { container } = await renderScreen(store)
    tap(widgetEl(container, 'wx'))
    const layer = container.querySelector('[data-detail-layer]')!
    expect(layer.querySelector('.tpl-weather__attribution')?.textContent).toContain('Open-Meteo')
  })

  it('详情层打开时布局删掉了该小组件，关闭详情层', async () => {
    const store = touchStore()
    const { container } = await renderScreen(store)
    tap(widgetEl(container, 'w2'))
    const pruned = defaultScreens()
    pruned[0] = screenOf('index', [textWidget('w1', 0, 0)])
    act(() => store.applyPatch(patchOf('layout', { resolved_layout: layoutOf(pruned, 2) })))
    expect(container.querySelector('[data-detail-layer]')).toBeNull()
  })

  it('左右滑动切换 screen', async () => {
    const { container, nav } = await renderScreen(touchStore())
    const root = container.querySelector('[data-screen-root]')!
    swipe(root, 600, 200)
    expect(nav.getState().screenId).toBe('s1')
    expect(widgetEl(container, 'w3')).not.toBeNull()
    swipe(root, 200, 600)
    expect(nav.getState().screenId).toBe('index')
  })

  it('滑动手势结束在小组件上不触发点击', async () => {
    const { container } = await renderScreen(touchStore())
    const el = widgetEl(container, 'w2')
    swipe(el, 500, 100)
    fireEvent.click(el)
    expect(container.querySelector('[data-detail-layer]')).toBeNull()
  })

  it('关屏后唤醒的第一次触摸只点亮不点击，第二次才点击（Ruling 33）', async () => {
    const store = touchStore()
    const { container } = await renderScreen(store)
    act(() => store.applyPatch(patchOf('screen_state', { screen_state: { mode: 'off', theme_id: 'ambient', reason: 'schedule' } })))
    act(() => store.applyPatch(patchOf('screen_state', { screen_state: { mode: 'on', theme_id: 'ambient', reason: 'wake' } })))
    tap(widgetEl(container, 'w2'))
    expect(container.querySelector('[data-detail-layer]')).toBeNull()
    tap(widgetEl(container, 'w2'))
    expect(container.querySelector('[data-detail-layer]')).not.toBeNull()
  })

  it('一直亮着时第一次触摸正常点击', async () => {
    const { container } = await renderScreen(touchStore())
    tap(widgetEl(container, 'w2'))
    expect(container.querySelector('[data-detail-layer]')).not.toBeNull()
  })

  it('详情层里没有数据的实例显示占位文案', async () => {
    const store = storeWith({ screen_data: [makeData([], { instance_id: 'i1' })], screen_settings: touchSettings() })
    const { container } = await renderScreen(store)
    tap(widgetEl(container, 'w2'))
    expect(container.querySelector('[data-detail-layer]')!.textContent).toContain('暂无数据')
  })
})

describe('屏幕根：数据更新', () => {
  it('screen_data patch 更新小组件读数', async () => {
    const store = storeWith({ screen_data: [makeData([temp, humidity])] })
    const { container } = await renderScreen(store)
    expect(container.querySelector('[data-widget-id="w2"]')!.textContent).toContain('21.5')
    act(() => store.applyPatch(patchOf('screen_data', { screen_data: [makeData([{ ...temp, value: 30 }, humidity])] })))
    expect(container.querySelector('[data-widget-id="w2"]')!.textContent).toContain('30')
  })

  it('snapshot 中无 screen_data 时小组件不报错', async () => {
    const { container } = await renderScreen(storeWith({ screen_data: undefined }))
    expect(widgetEl(container, 'w2')).not.toBeNull()
  })
})
