import type { Instance, LayoutWidget, PluginInfo, PluginWidgetSize, WidgetCatalog, WidgetSize } from '@/types/generated'

// 小组件库与尺寸白名单：插件小组件的尺寸来自各插件的 manifest，通用与聚合小组件来自服务端目录。

export type LibraryGroup = 'plugin' | 'generic' | 'aggregate'

export interface LibraryEntry {
  /** 库内唯一键 */
  key: string
  group: LibraryGroup
  /** 显示名：插件小组件名，或模板名 */
  title: string
  /** 副标题：插件 id 或模板名，搜索时也匹配 */
  subtitle: string
  pluginId?: string
  widgetId?: string
  template?: string
  sizes: WidgetSize[]
}

const sameSize = (a: WidgetSize, b: WidgetSize) => a.cols === b.cols && a.rows === b.rows

function pluginSizes(sizes: PluginWidgetSize[]): WidgetSize[] {
  return sizes.map((s) => ({ cols: s.cols, rows: s.rows }))
}

/** 按插件声明、通用、聚合三组生成库条目；notifier 类插件没有小组件 */
export function buildLibrary(
  plugins: readonly PluginInfo[],
  catalog: WidgetCatalog | null,
  templateTitle: (template: string) => string,
): LibraryEntry[] {
  const out: LibraryEntry[] = []
  for (const p of plugins) {
    for (const w of p.widgets ?? []) {
      if (w.sizes.length === 0) continue
      out.push({
        key: `plugin:${p.id}:${w.id}`,
        group: 'plugin',
        title: w.name,
        subtitle: p.id,
        pluginId: p.id,
        widgetId: w.id,
        sizes: pluginSizes(w.sizes),
      })
    }
  }
  for (const [group, entries] of [
    ['generic', catalog?.generic ?? []],
    ['aggregate', catalog?.aggregate ?? []],
  ] as const) {
    for (const e of entries) {
      out.push({
        key: `${group}:${e.template}`,
        group,
        title: templateTitle(e.template),
        subtitle: e.template,
        template: e.template,
        sizes: e.sizes,
      })
    }
  }
  return out
}

export function countByGroup(entries: readonly LibraryEntry[]): Record<LibraryGroup, number> {
  const n: Record<LibraryGroup, number> = { plugin: 0, generic: 0, aggregate: 0 }
  for (const e of entries) n[e.group]++
  return n
}

/** 某一组内按名称或副标题（插件 id）搜索，不区分大小写 */
export function filterLibrary(entries: readonly LibraryEntry[], group: LibraryGroup, query: string): LibraryEntry[] {
  const q = query.trim().toLowerCase()
  return entries.filter(
    (e) => e.group === group && (q === '' || e.title.toLowerCase().includes(q) || e.subtitle.toLowerCase().includes(q)),
  )
}

/** 布局里小组件找到对应的插件小组件尺寸声明 */
export function pluginWidgetSize(plugins: readonly PluginInfo[], w: LayoutWidget, size: WidgetSize = w.size): PluginWidgetSize | undefined {
  const decl = plugins.find((p) => p.id === w.plugin_id)?.widgets?.find((x) => x.id === w.widget_id)
  return decl?.sizes.find((s) => sameSize(s, size))
}

/**
 * 小组件允许的尺寸（按声明顺序）。插件或小组件声明已消失时只剩当前尺寸，保证引用失效的小组件仍可移动与删除。
 */
export function allowedSizesOf(w: LayoutWidget, plugins: readonly PluginInfo[], catalog: WidgetCatalog | null): WidgetSize[] {
  if (w.source === 'plugin') {
    const decl = plugins.find((p) => p.id === w.plugin_id)?.widgets?.find((x) => x.id === w.widget_id)
    return decl && decl.sizes.length ? pluginSizes(decl.sizes) : [w.size]
  }
  const list = w.source === 'aggregate' ? catalog?.aggregate : catalog?.generic
  const entry = list?.find((e) => e.template === w.template)
  return entry && entry.sizes.length ? entry.sizes : [w.size]
}

/** 选中小组件的模板：plugin 来源取自 manifest 尺寸声明 */
export function templateOf(w: LayoutWidget, plugins: readonly PluginInfo[]): string {
  if (w.source === 'plugin') return pluginWidgetSize(plugins, w)?.template ?? ''
  return w.template ?? ''
}

export function newWidgetId(existing: ReadonlySet<string>, random: () => number = Math.random): string {
  for (;;) {
    const id = `w${Math.floor(random() * 36 ** 6).toString(36).padStart(6, '0')}`
    if (!existing.has(id)) return id
  }
}

/** 同插件最早创建的实例（占位小组件解析时的绑定对象） */
export function earliestInstance(instances: readonly Instance[], pluginId: string | undefined): Instance | undefined {
  return instances
    .filter((i) => i.plugin_id === pluginId)
    .sort((a, b) => a.created_at.localeCompare(b.created_at))[0]
}

/** 从库条目生成新的放置记录：插件小组件默认绑定同插件最早的实例，通用与聚合的数据项由检查器指定 */
export function createWidget(
  entry: LibraryEntry,
  size: WidgetSize,
  at: { col: number; row: number },
  id: string,
  instances: readonly Instance[],
): LayoutWidget {
  const base = { id, size, col: at.col, row: at.row, options: {} }
  if (entry.group === 'plugin') {
    const inst = earliestInstance(instances, entry.pluginId)
    return {
      ...base,
      source: 'plugin',
      plugin_id: entry.pluginId,
      widget_id: entry.widgetId,
      binding: inst ? { instance_id: inst.id } : {},
    }
  }
  return { ...base, source: entry.group, template: entry.template, binding: { refs: [] } }
}
