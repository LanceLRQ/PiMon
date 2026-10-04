import type {
  Device,
  Instance,
  LayoutScreen,
  LayoutWidget,
  PluginInfo,
  ResolvedHost,
  ResolvedRef,
  ResolvedScreen,
  ResolvedWidget,
  ScreenInstanceData,
  InstanceDetail,
} from '@/types/generated'
import { earliestInstance, pluginWidgetSize } from './library'

// 画布渲染用的前端本地解析：把草稿里的放置记录解析成模板能渲染的 ResolvedWidget。
// 与服务端 resolve 的口径一致（占位绑定最早的实例、槽取自 manifest、聚合取最严重的状态），
// 但草稿随编辑实时变化，不能每次都去问服务端。

// 严重程度从高到低，聚合小组件取其中最严重的
const severity = ['critical', 'error', 'broken', 'warning', 'unknown', 'stale', 'offline', 'maintenance', 'paused', 'unconfigured', 'ok']

function worst(states: string[]): string {
  let best = states[0] ?? 'unconfigured'
  for (const s of states) {
    const a = severity.indexOf(s)
    const b = severity.indexOf(best)
    if ((a === -1 ? severity.length : a) < (b === -1 ? severity.length : b)) best = s
  }
  return best
}

export interface ResolveContext {
  plugins: readonly PluginInfo[]
  instances: readonly Instance[]
  /** 设备列表（不含中枢）；还没收到时为 null，灯组只有中枢一格 */
  devices?: readonly Device[] | null
}

const hostMetricsPlugin = 'host-metrics'
const hostCpuItem = 'cpu'

/** 实例比较：创建更早者优先，同时刻按 id 升序（与服务端 earlier 一致） */
function earlier(a: Instance, b: Instance): boolean {
  return a.created_at !== b.created_at ? a.created_at < b.created_at : a.id < b.id
}

/** 设备那一格的状态：维护优先；未在线且不兼容为 incompatible；其余取在线状态（空值按 pending），与服务端 hostState 一致 */
function hostState(d: Device): string {
  if (d.maintenance) return 'maintenance'
  if (d.incompatible && d.presence !== 'online') return 'incompatible'
  return d.presence || 'pending'
}

/**
 * 主机灯组各格，复刻服务端 hostCells：中枢固定第一、状态恒为 online；其后按设备列表顺序，只收已确认设备；
 * 每台机器的 CPU 来源取 runs_on 等于它、创建最早的 host-metrics 实例（暂停的也算，runs_on 为空按中枢）。
 */
export function hostCells(instances: readonly Instance[], devices: readonly Device[] | null | undefined): ResolvedHost[] {
  const cpu = new Map<string, Instance>()
  for (const i of instances) {
    if (i.plugin_id !== hostMetricsPlugin) continue
    const m = i.runs_on || 'hub'
    const cur = cpu.get(m)
    if (!cur || earlier(i, cur)) cpu.set(m, i)
  }
  const cell = (id: string, name: string, state: string): ResolvedHost => {
    const c = cpu.get(id)
    return { id, name, state, ...(c ? { instance_id: c.id } : {}) }
  }
  const out = [cell('hub', '', 'online')]
  for (const d of devices ?? []) if (d.confirmed) out.push(cell(d.id, d.name, hostState(d)))
  return out
}

function refTitle(plugins: readonly PluginInfo[], instances: readonly Instance[], instanceId: string, item: string): string | undefined {
  const inst = instances.find((i) => i.id === instanceId)
  const outputs = plugins.find((p) => p.id === inst?.plugin_id)?.outputs ?? []
  return outputs.find((o) => o.key === item)?.title
}

export function resolveWidget(w: LayoutWidget, ctx: ResolveContext): ResolvedWidget {
  const title = typeof w.options?.title === 'string' ? w.options.title : ''
  const common = { id: w.id, source: w.source, size: w.size, col: w.col, row: w.row, options: w.options ?? {} }
  const byId = (id: string | undefined) => ctx.instances.find((i) => i.id === id)

  if (w.source === 'plugin') {
    const plugin = ctx.plugins.find((p) => p.id === w.plugin_id)
    const decl = plugin?.widgets?.find((x) => x.id === w.widget_id)
    const sizeDecl = pluginWidgetSize(ctx.plugins, w)
    const wanted = w.binding.instance_id
    const placeholder = !wanted
    const inst = placeholder ? earliestInstance(ctx.instances, w.plugin_id) : byId(wanted)
    const slots: Record<string, ResolvedRef[]> = {}
    if (inst && sizeDecl) {
      for (const b of sizeDecl.bind) {
        slots[b.slot] = b.refs.map((r) => ({
          instance_id: inst.id,
          item: r.item,
          ...(r.field ? { field: r.field } : {}),
          title: refTitle(ctx.plugins, ctx.instances, inst.id, r.item),
        }))
      }
    }
    let display: string
    if (!inst) display = wanted ? 'broken' : 'unconfigured'
    else if (!plugin || !decl || !sizeDecl || inst.plugin_id !== w.plugin_id) display = 'broken'
    else display = inst.display_state
    return {
      ...common,
      plugin_id: w.plugin_id,
      widget_id: w.widget_id,
      template: sizeDecl?.template ?? '',
      instance_id: inst?.id,
      placeholder: placeholder && !!inst ? true : undefined,
      title: title || decl?.name || plugin?.name || '',
      slots,
      display_state: display,
    }
  }

  if (w.source === 'aggregate' && w.binding.data_source === 'hosts') {
    const hosts = hostCells(ctx.instances, ctx.devices)
    const items: ResolvedRef[] = hosts
      .filter((h) => h.instance_id)
      .map((h) => ({ instance_id: h.instance_id!, item: hostCpuItem, title: refTitle(ctx.plugins, ctx.instances, h.instance_id!, hostCpuItem) }))
    return { ...common, template: w.template ?? '', title, slots: { items }, display_state: 'ok', hosts }
  }

  const refs = w.binding.refs ?? []
  const resolvedRefs: ResolvedRef[] = refs.map((r) => ({
    instance_id: r.instance_id,
    item: r.item,
    ...(r.field ? { field: r.field } : {}),
    title: refTitle(ctx.plugins, ctx.instances, r.instance_id, r.item),
  }))
  const states = refs.map((r) => byId(r.instance_id)?.display_state ?? 'broken')
  return {
    ...common,
    template: w.template ?? '',
    title: title || (w.source === 'generic' ? (resolvedRefs[0]?.title ?? '') : ''),
    slots: { [w.source === 'aggregate' ? 'items' : 'value']: resolvedRefs },
    display_state: refs.length === 0 ? 'unconfigured' : worst(states),
  }
}

export function resolveScreen(screen: LayoutScreen, ctx: ResolveContext): ResolvedScreen {
  return {
    id: screen.id,
    name: screen.name,
    dwell_seconds: screen.dwell_seconds,
    in_rotation: screen.in_rotation,
    widgets: screen.widgets.map((w) => resolveWidget(w, ctx)),
  }
}

/** 画布需要取数的实例 id：被草稿里任一小组件引用到的（含占位解析出的实例） */
export function referencedInstanceIds(widgets: readonly ResolvedWidget[]): string[] {
  const ids = new Set<string>()
  for (const w of widgets) {
    if (w.instance_id) ids.add(w.instance_id)
    for (const refs of Object.values(w.slots)) for (const r of refs) ids.add(r.instance_id)
  }
  return [...ids].sort()
}

/** 把实例详情转成模板读取的数据；没拿到详情时按「从未成功采集」处理（items 为空，不当作零） */
export function toInstanceData(inst: Instance, detail: InstanceDetail | undefined): ScreenInstanceData {
  return {
    instance_id: inst.id,
    display_state: inst.display_state,
    report_status: detail?.report_status ?? inst.report_status,
    report_stale: detail?.report_stale ?? inst.report_stale,
    summary: inst.summary,
    last_success_at: inst.last_success_at,
    items: detail?.report?.items ?? [],
  }
}
