import { describe, expect, it } from 'vitest'
import type { ResolvedLayout, Settings } from '@/types/generated'
import type { Patch, Snapshot } from '@/types/protocol.generated'
import { PreviewSink, previewTopics, toScreenSettings } from './admin-preview'
import { createScreenStore } from './screen-store'
import { defaultScreens, layoutOf, makeData, temp } from './test-utils'

const settings = {
  language: 'en', timezone: 'Asia/Shanghai', access_url: 'http://secret', reduce_effects: true,
  screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'none', ui_scale: 1 },
} as unknown as Settings

const adminSnapshot = (over: Partial<Snapshot> = {}): Snapshot =>
  ({
    type: 'snapshot', build: 'b', server_time: '2026-10-01T06:00:00Z', role: 'admin', topics: previewTopics, settings, instances: [],
    screen_state: { mode: 'on', theme_id: 'industrial', reason: 'schedule' },
    screen_data: [makeData([temp])],
    ...over,
  }) as Snapshot

const patch = (over: Partial<Patch>): Patch => ({ type: 'patch', server_time: '2026-10-01T06:00:01Z', entity: 'x', ...over }) as Patch

describe('管理员预览适配', () => {
  it('完整设置只取屏幕用到的子集', () => {
    const s = toScreenSettings(settings)
    expect(s).toEqual({ language: 'en', timezone: 'Asia/Shanghai', reduce_effects: true, screen: settings.screen })
    expect(s).not.toHaveProperty('access_url')
  })

  it('订阅 screen_data（管理员默认没有）', () => {
    expect(previewTopics).toContain('screen_data')
    expect(previewTopics).not.toContain('instances')
  })

  it('snapshot：设置、屏幕状态、实例数据来自管理员 snapshot，布局用解析后的', () => {
    const store = createScreenStore()
    const resolved = layoutOf(defaultScreens(), 7)
    new PreviewSink(store, async () => resolved, resolved).applySnapshot(adminSnapshot())
    const s = store.getState()
    expect(s.synced).toBe(true)
    expect(s.layout?.version).toBe(7)
    expect(s.settings?.language).toBe('en')
    expect(s.screenState?.theme_id).toBe('industrial')
    expect(Object.keys(s.data)).toEqual(['i1'])
  })

  it('layout patch 重新取解析后的布局；晚到的旧结果被丢弃', async () => {
    const store = createScreenStore()
    const v1 = layoutOf(defaultScreens(), 1)
    const resolves: ((l: ResolvedLayout) => void)[] = []
    const sink = new PreviewSink(store, () => new Promise((r) => resolves.push(r)), v1)
    sink.applySnapshot(adminSnapshot())
    sink.applyPatch(patch({ entity: 'layout' }))
    sink.applyPatch(patch({ entity: 'layout' }))
    resolves[1](layoutOf(defaultScreens(), 3))
    await Promise.resolve()
    await Promise.resolve()
    resolves[0](layoutOf(defaultScreens(), 2))
    await Promise.resolve()
    await Promise.resolve()
    expect(store.getState().layout?.version).toBe(3)
  })

  it('取布局失败保留当前布局；实例类 patch 被忽略；设置、屏幕状态、数据 patch 透传', async () => {
    const store = createScreenStore()
    const v1 = layoutOf(defaultScreens(), 1)
    const sink = new PreviewSink(store, async () => Promise.reject(new Error('x')), v1)
    sink.applySnapshot(adminSnapshot())
    sink.applyPatch(patch({ entity: 'layout' }))
    sink.applyPatch(patch({ entity: 'instance_state' }))
    await Promise.resolve()
    expect(store.getState().layout?.version).toBe(1)
    sink.applyPatch(patch({ entity: 'screen_state', screen_state: { mode: 'off', theme_id: 'ambient', reason: 'remote_off' } }))
    sink.applyPatch(patch({ entity: 'screen_data', screen_data: [makeData([temp], { instance_id: 'i2' })] }))
    sink.applyPatch(patch({ entity: 'settings', settings: { ...settings, language: 'zh' } }))
    const s = store.getState()
    expect(s.screenState?.mode).toBe('off')
    expect(Object.keys(s.data).sort()).toEqual(['i1', 'i2'])
    expect(s.settings?.language).toBe('zh')
  })
})
