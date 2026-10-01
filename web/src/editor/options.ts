import type { Threshold } from '@/templates'

// 小组件显示选项（options）的读写约定：手动阈值固定为 {enabled, warning?, critical?, direction}。

export type OptionsPatch = Record<string, unknown>

/**
 * 合并选项补丁：值为 undefined、null 或空字符串的键一律删除。
 * 后端把 title 为 JSON null 判为非法，清空标题必须删键而不是写 null。
 */
export function applyOptionsPatch(options: Readonly<Record<string, unknown>>, patch: OptionsPatch): Record<string, unknown> {
  const next: Record<string, unknown> = { ...options }
  for (const [k, v] of Object.entries(patch)) {
    if (v === undefined || v === null || v === '') delete next[k]
    else next[k] = v
  }
  return next
}

/** 组装手动阈值：未填的阈值不写键 */
export function buildThreshold(t: Threshold): Record<string, unknown> {
  const out: Record<string, unknown> = { enabled: t.enabled, direction: t.direction }
  if (typeof t.warning === 'number' && Number.isFinite(t.warning)) out.warning = t.warning
  if (typeof t.critical === 'number' && Number.isFinite(t.critical)) out.critical = t.critical
  return out
}

/** 读取检查器用的阈值；格式不对时给出关闭状态的默认值 */
export function readThresholdForm(raw: unknown): Threshold {
  if (!raw || typeof raw !== 'object') return { enabled: false, direction: 'above' }
  const r = raw as Record<string, unknown>
  const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : undefined)
  return {
    enabled: r.enabled === true,
    warning: num(r.warning),
    critical: num(r.critical),
    direction: r.direction === 'below' ? 'below' : 'above',
  }
}
