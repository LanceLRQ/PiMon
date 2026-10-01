import type { Instance } from '@/types/generated'

// 实例表的查询语法、筛选与排序（总览与实例列表页共用）。
// 语法：status:、runs:、plugin: 加自由文本，空白分隔，键与值不区分大小写。

export type SortKey = 'status' | 'name' | 'run' | 'ago'
export type SortDir = 'asc' | 'desc'

export interface ParsedQuery {
  status?: string
  runs?: string
  plugin?: string
  text: string[]
}

// 状态别名到后端 display_state；中英文都认
const statusAliases: Record<string, string> = {
  ok: 'ok',
  正常: 'ok',
  warning: 'warning',
  warn: 'warning',
  警告: 'warning',
  critical: 'critical',
  crit: 'critical',
  严重: 'critical',
  error: 'error',
  fail: 'error',
  failed: 'error',
  失败: 'error',
  采集失败: 'error',
  stale: 'stale',
  过期: 'stale',
  unknown: 'unknown',
  unk: 'unknown',
  未知: 'unknown',
  offline: 'offline',
  离线: 'offline',
  maintenance: 'maintenance',
  维护中: 'maintenance',
  unconfigured: 'unconfigured',
  未配置: 'unconfigured',
  broken: 'broken',
  引用失效: 'broken',
}

export function normalizeStatus(value: string): string | null {
  return statusAliases[value.toLowerCase()] ?? null
}

export function parseQuery(q: string): ParsedQuery {
  const out: ParsedQuery = { text: [] }
  for (const tok of q.trim().split(/\s+/).filter(Boolean)) {
    const m = /^(status|runs|plugin):(.+)$/i.exec(tok)
    if (m) out[m[1].toLowerCase() as 'status' | 'runs' | 'plugin'] = m[2].toLowerCase()
    else out.text.push(tok.toLowerCase())
  }
  return out
}

// 「需关注」：与总览「需要处理」同一口径
export const attentionStates = ['critical', 'warning', 'error', 'stale'] as const

export function isAttention(state: string): boolean {
  return (attentionStates as readonly string[]).includes(state)
}

// 严重度：数值越大越靠前（状态列默认降序）
const ranks: Record<string, number> = { critical: 5, error: 4, warning: 3, stale: 2 }

export function statusRank(state: string): number {
  if (state in ranks) return ranks[state]
  return state === 'ok' ? 0 : 1
}

export interface TableFilter {
  attention: boolean
  // 运行位置（runs_on）精确匹配
  location: string | null
  // 插件 id 精确匹配
  plugin: string | null
  query: string
}

export const emptyFilter: TableFilter = { attention: false, location: null, plugin: null, query: '' }

// runs:hub 与 runs:agent 按类别匹配（非 hub 即 agent，与表格徽章一致）；其他值按运行位置（主机标识）包含匹配
function matchesRuns(runsOn: string, wanted: string): boolean {
  if (wanted === 'hub') return runsOn === 'hub'
  if (wanted === 'agent') return runsOn !== 'hub'
  return runsOn.toLowerCase().includes(wanted)
}

export function matchesFilter(inst: Instance, filter: TableFilter, parsed: ParsedQuery = parseQuery(filter.query)): boolean {
  if (filter.attention && !isAttention(inst.display_state)) return false
  if (filter.location && inst.runs_on !== filter.location) return false
  if (filter.plugin && inst.plugin_id !== filter.plugin) return false
  if (parsed.status !== undefined && normalizeStatus(parsed.status) !== inst.display_state) return false
  if (parsed.runs !== undefined && !matchesRuns(inst.runs_on, parsed.runs)) return false
  if (parsed.plugin !== undefined && !inst.plugin_id.toLowerCase().includes(parsed.plugin)) return false
  if (parsed.text.length === 0) return true
  const hay = `${inst.name} ${inst.plugin_id} ${inst.summary} ${inst.runs_on}`.toLowerCase()
  return parsed.text.every((t) => hay.includes(t))
}

export function filterInstances(list: Instance[], filter: TableFilter): Instance[] {
  const parsed = parseQuery(filter.query)
  return list.filter((i) => matchesFilter(i, filter, parsed))
}

function successMs(inst: Instance): number {
  const t = inst.last_success_at ? Date.parse(inst.last_success_at) : NaN
  return Number.isNaN(t) ? -Infinity : t
}

// 排序：状态列降序为严重优先；更新时间升序为最近更新在前，从未成功的视为最旧。
// 同值时一律按名称升序，保证顺序稳定。
export function sortInstances(list: Instance[], key: SortKey, dir: SortDir, locale = 'zh'): Instance[] {
  const sign = dir === 'asc' ? 1 : -1
  const byName = (a: Instance, b: Instance) => a.name.localeCompare(b.name, locale)
  return list.slice().sort((a, b) => {
    let v = 0
    switch (key) {
      case 'status':
        v = statusRank(a.display_state) - statusRank(b.display_state)
        break
      case 'name':
        v = byName(a, b)
        break
      case 'run':
        v = a.runs_on.localeCompare(b.runs_on, locale)
        break
      case 'ago': {
        const ta = successMs(a)
        const tb = successMs(b)
        v = ta === tb ? 0 : ta > tb ? -1 : 1
        break
      }
    }
    return v * sign || byName(a, b)
  })
}

// 默认排序方向：状态列从严重到轻，其余列升序
export function defaultDir(key: SortKey): SortDir {
  return key === 'status' ? 'desc' : 'asc'
}

// 状态统计（总览健康汇总）：未列出的状态（未知、离线、未配置等）归入 other
export interface StateCounts {
  total: number
  ok: number
  warning: number
  critical: number
  error: number
  stale: number
  other: number
}

export function countStates(list: Instance[]): StateCounts {
  const c: StateCounts = { total: list.length, ok: 0, warning: 0, critical: 0, error: 0, stale: 0, other: 0 }
  for (const i of list) {
    switch (i.display_state) {
      case 'ok':
      case 'warning':
      case 'critical':
      case 'error':
      case 'stale':
        c[i.display_state]++
        break
      default:
        c.other++
    }
  }
  return c
}
