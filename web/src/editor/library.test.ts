import { describe, expect, it } from 'vitest'
import type { Instance, LayoutWidget, PluginInfo, WidgetCatalog } from '@/types/generated'
import {
  allowedSizesOf, buildLibrary, countByGroup, createWidget, earliestInstance, filterLibrary, newWidgetId, templateOf,
} from './library'
import { referencedInstanceIds, resolveWidget, toInstanceData } from './resolve-draft'

const plugin = (id: string, widgets: PluginInfo['widgets'], outputs: PluginInfo['outputs'] = []): PluginInfo => ({
  id, version: '1', name: `插件 ${id}`, kind: 'source', runtime: 'builtin', origin: 'builtin', runs_on: ['hub'],
  interval_seconds: 30, min_interval_seconds: 0, timeout_seconds: 10, config_schema: [], outputs, widgets,
})
const bind = (slot: string, item: string) => ({ slot, list: false, refs: [{ item }] })
const plugins: PluginInfo[] = [
  plugin('demo', [
    { id: 'cpu', name: 'CPU', sizes: [
      { size: '1x1', cols: 1, rows: 1, template: 'gauge', bind: [bind('value', 'cpu.pi')] },
      { size: '2x1', cols: 2, rows: 1, template: 'value', bind: [bind('value', 'cpu.pi')] },
    ] },
  ], [{ key: 'cpu.pi', type: 'gauge', title: '树莓派 CPU' }]),
  plugin('quiet', []),
]
const catalog: WidgetCatalog = {
  generic: [{ template: 'value', sizes: [{ cols: 1, rows: 1 }, { cols: 2, rows: 1 }] }],
  aggregate: [{ template: 'status-grid', sizes: [{ cols: 2, rows: 2 }] }],
}
const inst = (id: string, pluginId: string, created: string, state = 'ok'): Instance => ({
  id, plugin_id: pluginId, name: id, runs_on: 'hub', interval_seconds: 0, effective_interval_seconds: 30, paused: false,
  display_state: state, summary: '', report_status: 'ok', report_stale: false, last_success_at: null, failures: 0,
  created_at: created, updated_at: created,
})
const instances = [inst('late', 'demo', '2026-02-01'), inst('early', 'demo', '2026-01-01', 'warning')]
const title = (t: string) => `T:${t}`

describe('小组件库', () => {
  const lib = buildLibrary(plugins, catalog, title)
  it('分三组，没有小组件的插件不出现', () => {
    expect(countByGroup(lib)).toEqual({ plugin: 1, generic: 1, aggregate: 1 })
    expect(lib.find((e) => e.group === 'generic')?.title).toBe('T:value')
  })
  it('搜索匹配名称与插件 id，且限定在所选分组内', () => {
    expect(filterLibrary(lib, 'plugin', 'cpu')).toHaveLength(1)
    expect(filterLibrary(lib, 'plugin', 'DEMO')).toHaveLength(1)
    expect(filterLibrary(lib, 'plugin', 'zzz')).toHaveLength(0)
    expect(filterLibrary(lib, 'generic', 'cpu')).toHaveLength(0)
    expect(filterLibrary(lib, 'aggregate', '')).toHaveLength(1)
  })
})

describe('允许尺寸与模板', () => {
  const w = (over: Partial<LayoutWidget>): LayoutWidget => ({
    id: 'a', source: 'plugin', plugin_id: 'demo', widget_id: 'cpu', size: { cols: 1, rows: 1 }, col: 0, row: 0, binding: {}, options: {}, ...over,
  })
  it('插件小组件取 manifest 声明，模板随尺寸变化', () => {
    expect(allowedSizesOf(w({}), plugins, catalog)).toEqual([{ cols: 1, rows: 1 }, { cols: 2, rows: 1 }])
    expect(templateOf(w({}), plugins)).toBe('gauge')
    expect(templateOf(w({ size: { cols: 2, rows: 1 } }), plugins)).toBe('value')
  })
  it('通用与聚合取目录；声明消失时只剩当前尺寸', () => {
    expect(allowedSizesOf(w({ source: 'generic', template: 'value' }), plugins, catalog)).toHaveLength(2)
    expect(allowedSizesOf(w({ source: 'aggregate', template: 'status-grid', size: { cols: 2, rows: 2 } }), plugins, catalog)).toEqual([{ cols: 2, rows: 2 }])
    expect(allowedSizesOf(w({ widget_id: 'gone', size: { cols: 3, rows: 1 } }), plugins, catalog)).toEqual([{ cols: 3, rows: 1 }])
    expect(allowedSizesOf(w({ source: 'generic', template: 'nope' }), plugins, null)).toEqual([{ cols: 1, rows: 1 }])
  })
})

describe('新建与解析', () => {
  it('newWidgetId 避开已有 id', () => {
    const seq = [0, 0, 0.5]
    let i = 0
    const id = newWidgetId(new Set(['w000000']), () => seq[i++])
    expect(id).not.toBe('w000000')
    expect(id).toMatch(/^w[0-9a-z]{6}$/)
  })
  it('最早创建的实例', () => {
    expect(earliestInstance(instances, 'demo')?.id).toBe('early')
    expect(earliestInstance(instances, 'none')).toBeUndefined()
  })
  it('插件小组件默认绑定最早实例；通用小组件带空 refs', () => {
    const lib = buildLibrary(plugins, catalog, title)
    const p = createWidget(lib[0], { cols: 1, rows: 1 }, { col: 2, row: 1 }, 'n1', instances)
    expect(p).toMatchObject({ source: 'plugin', plugin_id: 'demo', widget_id: 'cpu', col: 2, row: 1, binding: { instance_id: 'early' } })
    const g = createWidget(lib[1], { cols: 1, rows: 1 }, { col: 0, row: 0 }, 'n2', instances)
    expect(g).toMatchObject({ source: 'generic', template: 'value', binding: { refs: [] } })
  })
  it('占位解析到最早实例，槽与模板随尺寸，标题取 options 或声明名', () => {
    const r = resolveWidget({ id: 'a', source: 'plugin', plugin_id: 'demo', widget_id: 'cpu', size: { cols: 2, rows: 1 }, col: 0, row: 0, binding: {}, options: {} }, { plugins, instances })
    expect(r).toMatchObject({ template: 'value', instance_id: 'early', placeholder: true, title: 'CPU', display_state: 'warning' })
    expect(r.slots.value).toEqual([{ instance_id: 'early', item: 'cpu.pi', title: '树莓派 CPU' }])
    const t = resolveWidget({ id: 'a', source: 'plugin', plugin_id: 'demo', widget_id: 'cpu', size: { cols: 1, rows: 1 }, col: 0, row: 0, binding: { instance_id: 'late' }, options: { title: '我的' } }, { plugins, instances })
    expect(t).toMatchObject({ title: '我的', instance_id: 'late', display_state: 'ok' })
    expect(t.placeholder).toBeUndefined()
  })
  it('实例不存在为 broken，无实例可绑为 unconfigured，聚合取最严重', () => {
    const base = { id: 'a', source: 'plugin', plugin_id: 'demo', widget_id: 'cpu', size: { cols: 1, rows: 1 }, col: 0, row: 0, options: {} }
    expect(resolveWidget({ ...base, binding: { instance_id: 'ghost' } }, { plugins, instances }).display_state).toBe('broken')
    expect(resolveWidget({ ...base, binding: {} }, { plugins, instances: [] }).display_state).toBe('unconfigured')
    const agg = resolveWidget({ id: 'g', source: 'aggregate', template: 'status-grid', size: { cols: 2, rows: 2 }, col: 0, row: 0, options: {},
      binding: { refs: [{ instance_id: 'late', item: 'cpu.pi' }, { instance_id: 'early', item: 'cpu.pi' }] } }, { plugins, instances })
    expect(agg.display_state).toBe('warning')
    expect(agg.slots.items).toHaveLength(2)
  })
  it('引用的实例 id 去重排序；缺详情时 items 为空而不是零', () => {
    const g = resolveWidget({ id: 'g', source: 'generic', template: 'value', size: { cols: 1, rows: 1 }, col: 0, row: 0, options: {}, binding: { refs: [{ instance_id: 'late', item: 'cpu.pi' }] } }, { plugins, instances })
    expect(referencedInstanceIds([g, g])).toEqual(['late'])
    expect(toInstanceData(instances[0], undefined).items).toEqual([])
  })
})
