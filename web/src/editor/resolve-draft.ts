import type {
  Instance,
  LayoutScreen,
  LayoutWidget,
  PluginInfo,
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
