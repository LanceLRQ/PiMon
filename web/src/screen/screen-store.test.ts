import { describe, expect, it } from 'vitest'
import type { Patch, ScreenSettings, Snapshot } from '@/types/protocol.generated'
import type { ResolvedLayout, ScreenInstanceData, ScreenState } from '@/types/generated'
import { createScreenStore, screenNow } from './screen-store'

const settings = (over: Partial<ScreenSettings> = {}): ScreenSettings => ({
  language: 'zh',
  timezone: 'Asia/Shanghai',
  reduce_effects: false,
  screen: {
    carousel_mode: 'auto',
    idle_home_seconds: 60,
    default_dwell_seconds: 15,
    input_mode: 'auto',
    ui_scale: 1,
  },
  ...over,
})

const layout = (version: number, ids = ['index']): ResolvedLayout => ({
  version,
  grid: { cols: 8, rows: 5 },
  screens: ids.map((id) => ({ id, name: id, dwell_seconds: 0, in_rotation: true, widgets: [] })),
})

const state = (mode: string, theme = 'ambient'): ScreenState => ({ mode, theme_id: theme, reason: 'schedule' })

const inst = (id: string, summary = ''): ScreenInstanceData => ({
  instance_id: id,
  display_state: 'ok',
  report_status: 'ok',
  report_stale: false,
  summary,
  last_success_at: null,
  items: [],
})

const snapshot = (over: Partial<Snapshot> = {}): Snapshot => ({
  type: 'snapshot',
  build: 'b1',
  server_time: '2026-10-01T06:00:00Z',
  role: 'screen',
  topics: ['settings', 'layout', 'screen_state', 'screen_data'],
  screen_settings: settings(),
  resolved_layout: layout(3),
  screen_state: state('on'),
  screen_data: [inst('a'), inst('b')],
  instances: [],
  ...over,
})

const patch = (entity: string, over: Partial<Patch> = {}): Patch => ({
  type: 'patch',
  server_time: '2026-10-01T06:00:05Z',
  entity,
  ...over,
})

describe('屏幕 store', () => {
  it('snapshot 整体覆盖布局、状态、设置与数据', () => {
    const s = createScreenStore()
    s.applySnapshot(snapshot())
    const st = s.getState()
    expect(st.synced).toBe(true)
    expect(st.build).toBe('b1')
    expect(st.layout?.version).toBe(3)
    expect(st.screenState?.mode).toBe('on')
    expect(st.settings?.timezone).toBe('Asia/Shanghai')
    expect(Object.keys(st.data).sort()).toEqual(['a', 'b'])
    // 重连后 snapshot 覆盖本地数据：旧实例被丢弃
    s.applySnapshot(snapshot({ screen_data: [inst('c')], resolved_layout: layout(4) }))
    expect(Object.keys(s.getState().data)).toEqual(['c'])
    expect(s.getState().layout?.version).toBe(4)
  })

  it('layout patch 整体替换解析后布局，按到达顺序覆盖', () => {
    const s = createScreenStore()
    s.applySnapshot(snapshot())
    s.applyPatch(patch('layout', { resolved_layout: layout(5, ['index', 's1']) }))
    s.applyPatch(patch('layout', { resolved_layout: layout(6, ['index']) }))
    expect(s.getState().layout?.version).toBe(6)
    expect(s.getState().layout?.screens).toHaveLength(1)
  })

  it('screen_data patch 按 instance_id 合并覆盖，不影响其他实例', () => {
    const s = createScreenStore()
    s.applySnapshot(snapshot())
    s.applyPatch(patch('screen_data', { screen_data: [inst('a', 'new'), inst('z', 'fresh')] }))
    const d = s.getState().data
    expect(d.a.summary).toBe('new')
    expect(d.b.summary).toBe('')
    expect(d.z.summary).toBe('fresh')
  })

  it('screen_state 与 settings patch 替换对应字段', () => {
    const s = createScreenStore()
    s.applySnapshot(snapshot())
    s.applyPatch(patch('screen_state', { screen_state: state('off', 'industrial') }))
    expect(s.getState().screenState).toMatchObject({ mode: 'off', theme_id: 'industrial' })
    s.applyPatch(patch('settings', { screen_settings: settings({ timezone: 'UTC' }) }))
    expect(s.getState().settings?.timezone).toBe('UTC')
  })

  it('缺少载荷的 patch 与未知实体被忽略', () => {
    const s = createScreenStore()
    s.applySnapshot(snapshot())
    const before = s.getState()
    s.applyPatch(patch('layout'))
    s.applyPatch(patch('screen_state'))
    s.applyPatch(patch('mystery'))
    expect(s.getState().layout).toBe(before.layout)
    expect(s.getState().screenState).toBe(before.screenState)
  })

  it('服务端时间校正与连接状态', () => {
    const s = createScreenStore()
    const real = Date.now()
    s.applyServerTime(new Date(real + 5000).toISOString())
    expect(Math.abs(screenNow(s) - (real + 5000))).toBeLessThan(200)
    s.setConnected(true)
    expect(s.getState().connected).toBe(true)
    s.setConnected(false)
    expect(s.getState().connected).toBe(false)
  })

  it('记录最近一次收到数据的服务端时间，供断线角标使用', () => {
    const s = createScreenStore()
    expect(s.getState().lastDataAt).toBeNull()
    s.applySnapshot(snapshot())
    expect(s.getState().lastDataAt).toBe(Date.parse('2026-10-01T06:00:00Z'))
    s.applyPatch(patch('screen_data', { screen_data: [inst('a', 'x')] }))
    expect(s.getState().lastDataAt).toBe(Date.parse('2026-10-01T06:00:05Z'))
  })

  it('订阅者在变化时被通知', () => {
    const s = createScreenStore()
    let n = 0
    const off = s.subscribe(() => n++)
    s.applySnapshot(snapshot())
    s.setConnected(true)
    expect(n).toBe(2)
    off()
    s.setConnected(false)
    expect(n).toBe(2)
  })
})
