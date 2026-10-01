import type { Item, ResolvedRef, ResolvedWidget, ScreenInstanceData, WidgetRef } from '@/types/generated'
import type { StatusLevel } from '@/themes/types'
import type { InstanceDataMap, Threshold } from './types'

/** 取槽里的第一个引用；多个槽名按顺序尝试（plugin 来源槽名各异，generic 固定为 value） */
export function slotRef(widget: ResolvedWidget, ...slots: string[]): WidgetRef | undefined {
  for (const name of slots) {
    const ref = widget.slots[name]?.[0]
    if (ref) return ref
  }
  return undefined
}

export function findItem(ref: WidgetRef | undefined, data: InstanceDataMap): Item | undefined {
  if (!ref) return undefined
  return data[ref.instance_id]?.items.find((i) => i.key === ref.item)
}

/** 小组件所属的主实例：plugin 来源取绑定实例，否则取第一个引用的实例 */
export function primaryData(widget: ResolvedWidget, data: InstanceDataMap, ref?: WidgetRef): ScreenInstanceData | undefined {
  const id = widget.instance_id ?? ref?.instance_id ?? Object.values(widget.slots).flat()[0]?.instance_id
  return id ? data[id] : undefined
}

function finite(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}

/** 读数值字段；缺失返回 null（缺失不当作 0）。field 为空取该类型的默认字段 */
export function readNumber(item: Item, field?: string): number | null {
  if (field) {
    const v = (item as unknown as Record<string, unknown>)[field]
    return finite(v) ? v : null
  }
  const v = item.type === 'money' ? item.amount : item.type === 'quota' ? item.remaining_pct : item.value
  return finite(v) ? v : null
}

/** report 状态取值转成主题的五级之一，非法值按 unknown */
export function levelOfValue(state: string | undefined): StatusLevel {
  return state === 'ok' || state === 'warning' || state === 'critical' ? state : 'unknown'
}

/** 数值落在哪一级；未开启或未设任何阈值返回 null */
export function thresholdLevel(value: number, t: Threshold | undefined): StatusLevel | null {
  if (!t || !t.enabled) return null
  if (!finite(t.warning) && !finite(t.critical)) return null
  const hit = (limit: number | undefined) =>
    finite(limit) && (t.direction === 'below' ? value <= limit : value >= limit)
  if (hit(t.critical)) return 'critical'
  if (hit(t.warning)) return 'warning'
  return 'ok'
}

/** options.threshold 的宽松解析，格式不对视为未设置 */
export function parseThreshold(raw: unknown): Threshold | undefined {
  if (!raw || typeof raw !== 'object') return undefined
  const r = raw as Record<string, unknown>
  return {
    enabled: r.enabled === true,
    warning: finite(r.warning) ? r.warning : undefined,
    critical: finite(r.critical) ? r.critical : undefined,
    direction: r.direction === 'below' ? 'below' : 'above',
  }
}

/**
 * Ruling 32 的取色顺序：手动阈值 > manifest 默认阈值 > state 项按其 state > 其余中性（null）。
 */
export function resolveValueLevel(
  item: Item | undefined,
  field: string | undefined,
  manual: Threshold | undefined,
  fallback: Threshold | undefined,
): StatusLevel | null {
  if (!item) return null
  if (item.type === 'state') return levelOfValue(item.state)
  const v = readNumber(item, field)
  if (v === null) return null
  return thresholdLevel(v, manual) ?? thresholdLevel(v, fallback)
}

/** net-reach 的 target[*] 是成功率：100 正常、0 严重、其余警告（成功率本身没有手动阈值时的约定） */
export function reachLevel(value: number): StatusLevel {
  return value >= 100 ? 'ok' : value <= 0 ? 'critical' : 'warning'
}

export function isReach(item: Item): boolean {
  return item.type === 'gauge' && item.key.startsWith('target[')
}

/** 以 [*] 结尾的通配引用：绑定一族动态键（如 disk[*]、target[*]） */
export function isWildcardRef(ref: { item: string }): boolean {
  return ref.item.endsWith('[*]')
}

/**
 * 把通配引用按实例数据项展开为全部成员（键形如 base[名]），其余引用原样保留。
 * 成员不带服务端标题（标题属于通配项本身），名称由成员 label 或方括号里的名字给出。
 */
export function expandRefs(refs: readonly ResolvedRef[], data: InstanceDataMap): ResolvedRef[] {
  const out: ResolvedRef[] = []
  for (const ref of refs) {
    if (!isWildcardRef(ref)) {
      out.push(ref)
      continue
    }
    const prefix = ref.item.slice(0, -2)
    for (const item of data[ref.instance_id]?.items ?? []) {
      if (item.key.startsWith(prefix) && item.key.endsWith(']') && item.key.length > prefix.length + 1) {
        out.push({ instance_id: ref.instance_id, item: item.key, field: ref.field })
      }
    }
  }
  return out
}

/** 引用里是否含通配、以及对应实例数据是否已到达（用于区分「没有成员」与「还没有数据」） */
export function hasWildcardWithData(refs: readonly ResolvedRef[], data: InstanceDataMap): boolean {
  return refs.some((r) => isWildcardRef(r) && data[r.instance_id] !== undefined)
}

/**
 * 数据项自身的级别（外框着色用，Ruling 47）：条目错误为 error；state 项按其 state；
 * target[*] 成功率按约定分级；其余数值项按手动阈值，无阈值为 null（中性）。缺失的数据项为 null。
 */
export function itemLevel(item: Item | undefined, field: string | undefined, manual: Threshold | undefined, fallback: Threshold | undefined): StatusLevel | null {
  if (!item) return null
  if (item.error) return 'error'
  if (item.type === 'state') return levelOfValue(item.state)
  if (isReach(item)) {
    const v = readNumber(item, field)
    return v === null ? null : reachLevel(v)
  }
  return resolveValueLevel(item, field, manual, fallback)
}

const levelRank: Record<StatusLevel, number> = { ok: 0, unknown: 1, warning: 2, error: 3, critical: 4 }

/** 取最严重的级别；没有任何级别为 null */
export function worstLevel(levels: readonly (StatusLevel | null)[]): StatusLevel | null {
  let worst: StatusLevel | null = null
  for (const l of levels) if (l && (worst === null || levelRank[l] > levelRank[worst])) worst = l
  return worst
}

/** 小组件自身绑定的全部数据项（含通配展开）里最严重的级别 */
export function widgetOwnLevel(widget: ResolvedWidget, data: InstanceDataMap, manual: Threshold | undefined, fallback: Threshold | undefined): StatusLevel | null {
  const refs = expandRefs(Object.values(widget.slots).flat(), data)
  return worstLevel(refs.map((r) => itemLevel(findItem(r, data), r.field, manual, fallback)))
}
