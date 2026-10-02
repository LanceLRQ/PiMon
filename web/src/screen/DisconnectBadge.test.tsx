import { act, render, screen } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from '@/i18n'
import { DisconnectBadge } from './DisconnectBadge'
import { createScreenStore, type ScreenStore } from './screen-store'
import { settingsOf, snapshotOf } from './test-utils'

// 数据时间 06:00 UTC = 北京 14:00
const dataTime = '2026-10-01T06:00:00Z'

async function mount(over: Parameters<typeof snapshotOf>[0] = {}, lng: 'zh' | 'en' = 'zh', existing?: ScreenStore) {
  const i18n = await createI18n(lng)
  // 先定下「现在」再收 snapshot：服务端时钟校正是按收到时的本机时间算的
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
  vi.setSystemTime(new Date('2026-10-01T06:30:00Z'))
  const store = existing ?? createScreenStore()
  if (!existing) store.applySnapshot(snapshotOf({ server_time: dataTime, ...over }))
  const view = render(
    <I18nextProvider i18n={i18n}>
      <DisconnectBadge store={store} graceMs={3000} />
    </I18nextProvider>,
  )
  const advance = (ms: number) => act(async () => void vi.advanceTimersByTime(ms))
  return { store, advance, ...view }
}

afterEach(() => vi.useRealTimers())

describe('断线角标', () => {
  beforeEach(() => vi.useRealTimers())

  it('连接中断超过宽限后出现，显示数据时间（屏幕设置时区）', async () => {
    const { store, advance } = await mount()
    act(() => store.setConnected(false))
    expect(screen.queryByRole('status')).toBeNull()
    await advance(3000)
    expect(screen.getByRole('status')).toHaveTextContent('连接中断，显示 14:00 的数据')
  })

  it('连着时不显示；重连并收到 snapshot 后角标消失', async () => {
    const { store, advance } = await mount()
    act(() => store.setConnected(true))
    await advance(10_000)
    expect(screen.queryByRole('status')).toBeNull()

    act(() => store.setConnected(false))
    await advance(3000)
    expect(screen.getByRole('status')).toBeInTheDocument()

    act(() => {
      store.setConnected(true)
      store.applySnapshot(snapshotOf({ server_time: '2026-10-01T06:31:00Z' }))
    })
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('宽限内恢复连接不闪现', async () => {
    const { store, advance } = await mount()
    act(() => store.setConnected(false))
    await advance(1000)
    act(() => store.setConnected(true))
    await advance(5000)
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('按设置时区与语言：英文文案，时区不同时刻不同', async () => {
    const { advance, store } = await mount({ screen_settings: settingsOf({}, { timezone: 'UTC' }) }, 'en')
    act(() => store.setConnected(false))
    await advance(3000)
    expect(screen.getByRole('status')).toHaveTextContent('Connection lost, showing data from 06:00')
  })

  it('数据不是当天的带上日期', async () => {
    // 数据比「现在」早两天（例如页面重载后用存档恢复）
    const online = createScreenStore()
    online.applySnapshot(snapshotOf())
    const o = online.getState()
    const restored = createScreenStore()
    restored.restore({ v: 1, savedAt: 0, lastDataAt: Date.parse('2026-09-29T06:00:00Z'), settings: o.settings, layout: o.layout, screenState: o.screenState, data: o.data })
    const { advance } = await mount({}, 'zh', restored)
    await advance(3000)
    expect(screen.getByRole('status').textContent).toMatch(/9\/29 14:00/)
  })

  it('没有任何数据时不显示（交给「hub 未运行」页）；关屏时不显示', async () => {
    const { store, advance } = await mount({ resolved_layout: undefined })
    act(() => store.setConnected(false))
    await advance(5000)
    expect(screen.queryByRole('status')).toBeNull()

    act(() => store.applySnapshot(snapshotOf({ server_time: dataTime, screen_state: { mode: 'off', theme_id: 'ambient', reason: 'schedule' } })))
    await advance(5000)
    expect(screen.queryByRole('status')).toBeNull()
  })
})
