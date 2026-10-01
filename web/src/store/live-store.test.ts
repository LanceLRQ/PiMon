import { beforeEach, describe, expect, it } from 'vitest'
import type { Instance, Settings } from '@/types/generated'
import type { Patch, Snapshot } from '@/types/protocol.generated'
import { createLiveStore, serverNow, type LiveStore } from './live-store'

function inst(id: string, extra: Partial<Instance> = {}): Instance {
  return {
    id,
    plugin_id: 'http-check',
    name: id,
    runs_on: 'hub',
    interval_seconds: 0,
    effective_interval_seconds: 60,
    paused: false,
    display_state: 'ok',
    summary: '',
    report_status: 'ok',
    report_stale: false,
    failures: 0,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    ...extra,
  }
}

const settings = { language: 'zh' } as Settings

function snapshot(instances: Instance[], extra: Partial<Snapshot> = {}): Snapshot {
  return {
    type: 'snapshot',
    build: 'b1',
    server_time: '2026-10-01T00:00:00Z',
    role: 'admin',
    topics: ['instances', 'settings'],
    settings,
    instances,
    ...extra,
  }
}

function patch(p: Partial<Patch>): Patch {
  return { type: 'patch', server_time: '2026-10-01T00:00:01Z', entity: 'instance_state', ...p }
}

describe('实时 store', () => {
  let store: LiveStore
  beforeEach(() => {
    store = createLiveStore()
  })

  it('snapshot 整体覆盖已有实例与设置', () => {
    store.applySnapshot(snapshot([inst('a'), inst('b')]))
    expect(store.getState().instances.map((i) => i.id)).toEqual(['a', 'b'])
    expect(store.getState().synced).toBe(true)
    expect(store.getState().build).toBe('b1')
    store.applySnapshot(snapshot([inst('c')], { build: 'b2' }))
    expect(store.getState().instances.map((i) => i.id)).toEqual(['c'])
    expect(store.getState().build).toBe('b2')
  })

  it('instance_state 按 id 覆盖，不存在则追加', () => {
    store.applySnapshot(snapshot([inst('a'), inst('b')]))
    store.applyPatch(patch({ instance: inst('a', { display_state: 'critical' }) }))
    expect(store.getState().instances.map((i) => [i.id, i.display_state])).toEqual([
      ['a', 'critical'],
      ['b', 'ok'],
    ])
    store.applyPatch(patch({ instance: inst('n') }))
    expect(store.getState().instances.map((i) => i.id)).toEqual(['a', 'b', 'n'])
  })

  it('instance_removed 删除指定实例，id 不存在时不产生新引用', () => {
    store.applySnapshot(snapshot([inst('a'), inst('b')]))
    store.applyPatch(patch({ entity: 'instance_removed', id: 'a' }))
    expect(store.getState().instances.map((i) => i.id)).toEqual(['b'])
    const before = store.getState().instances
    store.applyPatch(patch({ entity: 'instance_removed', id: 'zzz' }))
    expect(store.getState().instances).toBe(before)
  })

  it('settings patch 替换设置；未知实体被忽略', () => {
    store.applySnapshot(snapshot([]))
    store.applyPatch(patch({ entity: 'settings', settings: { ...settings, language: 'en' } }))
    expect(store.getState().settings?.language).toBe('en')
    store.applyPatch(patch({ entity: 'future_thing' }))
    expect(store.getState().settings?.language).toBe('en')
  })

  it('通知订阅者，取消订阅后不再通知', () => {
    let n = 0
    const off = store.subscribe(() => n++)
    store.applySnapshot(snapshot([]))
    store.setConnected(true)
    expect(n).toBe(2)
    off()
    store.setConnected(false)
    expect(n).toBe(2)
  })

  it('用服务端时间校正时钟偏差', () => {
    const skew = 5 * 60 * 1000
    store.applySnapshot(snapshot([], { server_time: new Date(Date.now() + skew).toISOString() }))
    expect(Math.abs(serverNow(store) - (Date.now() + skew))).toBeLessThan(1000)
    store.applyServerTime(new Date(Date.now() - skew).toISOString())
    expect(Math.abs(serverNow(store) - (Date.now() - skew))).toBeLessThan(1000)
  })

  it('reset 回到初始状态', () => {
    store.applySnapshot(snapshot([inst('a')]))
    store.reset()
    expect(store.getState().synced).toBe(false)
    expect(store.getState().instances).toEqual([])
  })
})
