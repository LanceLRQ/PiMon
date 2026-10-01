import type { Item, ResolvedWidget, ScreenInstanceData, WidgetRef } from '@/types/generated'
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
