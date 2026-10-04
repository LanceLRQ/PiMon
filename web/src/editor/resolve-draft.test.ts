import { describe, expect, it } from 'vitest'
import { device } from '@/pages/devices/test-utils'
import type { Instance, LayoutWidget } from '@/types/generated'
import { resolveWidget } from './resolve-draft'

// 灯组规则与服务端 hosts.go 的 hostCells/hostState 对照：同一份输入，顺序、状态与 instance_id 必须一致。

const inst = (id: string, pluginId: string, runsOn: string, created: string, extra: Partial<Instance> = {}): Instance => ({
  id, plugin_id: pluginId, name: id, runs_on: runsOn, interval_seconds: 0, effective_interval_seconds: 30, paused: false,
  display_state: 'ok', summary: '', report_status: 'ok', report_stale: false, last_success_at: null, failures: 0,
  created_at: created, updated_at: created, ...extra,
})
const plugins = [{ id: 'host-metrics', outputs: [{ key: 'cpu', type: 'gauge', title: 'CPU' }] }] as never[]
const w: LayoutWidget = {
  id: 'h', source: 'aggregate', template: 'status-grid', size: { cols: 2, rows: 2 }, col: 0, row: 0,
  binding: { data_source: 'hosts' }, options: {},
}

describe('草稿解析：主机灯组', () => {
  const instances = [
    inst('m-late', 'host-metrics', '', '2026-02-01T00:00:00Z'),
    inst('m-hub', 'host-metrics', 'hub', '2026-01-01T00:00:00Z'),
    inst('other', 'net-reach', 'hub', '2025-01-01T00:00:00Z'),
    inst('m-a-late', 'host-metrics', 'a', '2026-03-01T00:00:00Z'),
    inst('m-a', 'host-metrics', 'a', '2026-01-01T00:00:00Z', { paused: true }),
  ]
  const devices = [
    device('a'),
    device('pending', { confirmed: false }),
    device('b', { presence: 'offline', incompatible: { reason: 'version_too_old' } as never }),
    device('c', { maintenance: true, presence: 'offline' }),
    device('d', { presence: 'online', incompatible: { reason: 'version_too_old' } as never }),
    device('e', { presence: '' }),
  ]

  it('中枢第一、设备按列表顺序、只收已确认；CPU 取该机器最早的 host-metrics 实例（含暂停）', () => {
    const r = resolveWidget(w, { plugins, instances, devices })
    expect(r.hosts?.map((h) => [h.id, h.instance_id ?? ''])).toEqual([
      ['hub', 'm-hub'], ['a', 'm-a'], ['b', ''], ['c', ''], ['d', ''], ['e', ''],
    ])
    expect(r.hosts?.[0].name).toBe('')
    expect(r.hosts?.[1].name).toBe('a')
  })

  it('每格状态：维护优先，未在线且不兼容为 incompatible，在线忽略残留记录，空值为 pending', () => {
    const r = resolveWidget(w, { plugins, instances, devices })
    expect(r.hosts?.map((h) => h.state)).toEqual(['online', 'online', 'incompatible', 'maintenance', 'online', 'pending'])
  })

  it('整块状态固定 ok，items 槽按灯组顺序放 cpu 引用，不再是 unconfigured', () => {
    const r = resolveWidget(w, { plugins, instances, devices })
    expect(r.display_state).toBe('ok')
    expect(r.slots.items.map((x) => [x.instance_id, x.item])).toEqual([['m-hub', 'cpu'], ['m-a', 'cpu']])
    expect(r.slots.items[0].title).toBe('CPU')
  })

  it('设备列表还没收到或没有设备时只有中枢一格；没有实例时中枢无 CPU 引用', () => {
    const r = resolveWidget(w, { plugins, instances: [], devices: null })
    expect(r.hosts).toEqual([{ id: 'hub', name: '', state: 'online' }])
    expect(r.slots.items).toEqual([])
    expect(r.display_state).toBe('ok')
  })
})
