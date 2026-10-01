import type { Schedule, ScheduleProblem } from '@/types/generated'

// 时段计划的纯逻辑：草稿编辑、与后端一致的校验、服务端时区下的「现在」与下一次变化。
// 后端规则见 src/internal/hub/screenstate/schedule.go：时段 HH:MM 左闭右开，结束不晚于开始表示跨日，
// 只有一个时段时开始等于结束表示全天；时段互不重叠并覆盖 24 小时。

export const DAY = 24 * 60
export const MAX_PERIODS = 48
export const OFF = 'off'
export const THEME_ORDER = ['ambient', 'mission-control', 'industrial'] as const

/** 草稿时段：分钟数表示，key 在编辑期间保持稳定，用于选中与列表渲染 */
export interface DraftPeriod {
  key: string
  start: number
  end: number
  theme: string
}

let keySeq = 0
const newKey = () => `p${++keySeq}`

/** 严格解析 HH:MM（也接受 H:MM），24:00 视为一天结束（0）；非法返回 null */
export function parseHM(s: string): number | null {
  const m = /^(\d{1,2}):(\d{2})$/.exec(s.trim())
  if (!m) return null
  const h = Number(m[1])
  const min = Number(m[2])
  if (h === 24 && min === 0) return 0
  if (h > 23 || min > 59) return null
  return h * 60 + min
}

export function formatHM(minutes: number): string {
  const m = ((Math.round(minutes) % DAY) + DAY) % DAY
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
}

/** 区间端点的文案：一天结束写作 24:00（与后端校验问题的格式一致） */
export function formatBound(minutes: number): string {
  return minutes === DAY ? '24:00' : formatHM(minutes)
}

/** 时段长度（分钟，1–1440）；开始等于结束视为全天 */
export function lengthOf(p: Pick<DraftPeriod, 'start' | 'end'>): number {
  return (((p.end - p.start) % DAY) + DAY) % DAY || DAY
}

const byStart = (a: DraftPeriod, b: DraftPeriod) => a.start - b.start

export function fromServer(s: Schedule): DraftPeriod[] {
  const out: DraftPeriod[] = s.periods.map((p) => ({
    key: newKey(),
    start: parseHM(p.start) ?? NaN,
    end: parseHM(p.end) ?? NaN,
    theme: p.theme,
  }))
  return out.sort(byStart)
}

export function toServer(ps: readonly DraftPeriod[]): Schedule {
  return { periods: [...ps].sort(byStart).map((p) => ({ start: formatHM(p.start), end: formatHM(p.end), theme: p.theme })) }
}

/** 草稿是否与基线相同（按提交形状比较，忽略 key） */
export function sameSchedule(a: readonly DraftPeriod[], b: readonly DraftPeriod[]): boolean {
  return JSON.stringify(toServer(a)) === JSON.stringify(toServer(b))
}

const validThemes = new Set<string>([OFF, ...THEME_ORDER])

/** 与后端 ValidateSchedule 同口径：格式、主题、互不重叠、覆盖完整 24 小时 */
export function validate(ps: readonly DraftPeriod[]): ScheduleProblem[] {
  if (ps.length === 0) return [{ kind: 'empty' }]
  if (ps.length > MAX_PERIODS) return [{ kind: 'format' }]
  const problems: ScheduleProblem[] = []
  const segs: { from: number; to: number }[] = []
  ps.forEach((p, period) => {
    if (!Number.isFinite(p.start) || !Number.isFinite(p.end)) {
      problems.push({ kind: 'format', period })
      return
    }
    if (!validThemes.has(p.theme)) {
      problems.push({ kind: 'theme', period })
      return
    }
    const end = p.start + lengthOf(p)
    if (end > DAY) segs.push({ from: p.start, to: DAY }, { from: 0, to: end - DAY })
    else segs.push({ from: p.start, to: end })
  })
  if (problems.length > 0) return problems
  segs.sort((a, b) => a.from - b.from || a.to - b.to)
  let cursor = 0
  for (const sg of segs) {
    if (sg.from > cursor) problems.push({ kind: 'gap', from: formatBound(cursor), to: formatBound(sg.from) })
    else if (sg.from < cursor) problems.push({ kind: 'overlap', from: formatBound(sg.from), to: formatBound(Math.min(cursor, sg.to)) })
    cursor = Math.max(cursor, sg.to)
  }
  if (cursor < DAY) problems.push({ kind: 'gap', from: formatBound(cursor), to: '24:00' })
  return problems
}

const indexOfKey = (ps: readonly DraftPeriod[], key: string) => ps.findIndex((p) => p.key === key)

/** 改开始时间：前一段若首尾相接则结束跟随，保持无缝；单段（全天）起止一起动 */
export function setStart(ps: readonly DraftPeriod[], key: string, minute: number): DraftPeriod[] {
  const i = indexOfKey(ps, key)
  if (i < 0) return [...ps]
  const old = ps[i]
  const next = ps.map((p) => ({ ...p }))
  next[i].start = minute
  if (ps.length === 1) next[i].end = minute
  else {
    const prev = (i - 1 + ps.length) % ps.length
    if (ps[prev].end === old.start) next[prev].end = minute
  }
  return next.sort(byStart)
}

/** 改结束时间：后一段若首尾相接则开始跟随 */
export function setEnd(ps: readonly DraftPeriod[], key: string, minute: number): DraftPeriod[] {
  const i = indexOfKey(ps, key)
  if (i < 0) return [...ps]
  const old = ps[i]
  const next = ps.map((p) => ({ ...p }))
  next[i].end = minute
  if (ps.length === 1) next[i].start = minute
  else {
    const following = (i + 1) % ps.length
    if (ps[following].start === old.end) next[following].start = minute
  }
  return next.sort(byStart)
}

export function setTheme(ps: readonly DraftPeriod[], key: string, theme: string): DraftPeriod[] {
  return ps.map((p) => (p.key === key ? { ...p, theme } : { ...p }))
}

/** 把某段起点的边界移动 delta 分钟：前一段随之伸缩，两段各至少保留 1 分钟；单段没有边界 */
export function moveBoundary(ps: readonly DraftPeriod[], key: string, delta: number): DraftPeriod[] {
  const i = indexOfKey(ps, key)
  if (i < 0 || ps.length < 2) return ps as DraftPeriod[]
  const prev = (i - 1 + ps.length) % ps.length
  const total = lengthOf(ps[prev]) + lengthOf(ps[i])
  // 两段总长为一整天时（只有这两段）两个边界都可动，总长按 1440 计，夹取范围不变
  const prevLen = Math.min(Math.max(lengthOf(ps[prev]) + delta, 1), total - 1)
  const start = (ps[prev].start + prevLen) % DAY
  const next = ps.map((p) => ({ ...p }))
  next[i].start = start
  next[prev].end = start
  return next.sort(byStart)
}

export function removePeriod(ps: readonly DraftPeriod[], key: string): DraftPeriod[] {
  const i = indexOfKey(ps, key)
  if (i < 0 || ps.length <= 1) return ps as DraftPeriod[]
  const prev = (i - 1 + ps.length) % ps.length
  const next = ps.map((p) => ({ ...p }))
  next[prev].end = ps[i].end
  next.splice(i, 1)
  return next.sort(byStart)
}

/** 某一分钟落在哪一段（左闭右开）；计划有缺口或重叠时取第一个命中的，没有返回 -1 */
export function locate(ps: readonly DraftPeriod[], minute: number): number {
  return ps.findIndex((p) => {
    const off = (((minute - p.start) % DAY) + DAY) % DAY
    return off < lengthOf(p)
  })
}

function nextTheme(theme: string): string {
  if (theme === OFF) return OFF
  const i = THEME_ORDER.indexOf(theme as (typeof THEME_ORDER)[number])
  return THEME_ORDER[(i + 1) % THEME_ORDER.length]
}

export interface SplitResult {
  periods: DraftPeriod[]
  newKey: string
}

/** 在 minute 处拆分它落入的那一段：后半段是新时段（关屏段拆出的仍是关屏，其余换成下一个主题）。
 *  minute 恰是某段起点、或所在段不足 2 分钟时无法拆分，返回 null。 */
export function splitAt(ps: readonly DraftPeriod[], minute: number, theme?: string): SplitResult | null {
  const i = locate(ps, minute)
  if (i < 0) return null
  const host = ps[i]
  if (minute === host.start || lengthOf(host) < 2) return null
  const created: DraftPeriod = { key: newKey(), start: minute, end: host.end, theme: theme ?? nextTheme(host.theme) }
  const next = ps.map((p) => ({ ...p }))
  next[i].end = minute
  next.push(created)
  return { periods: next.sort(byStart), newKey: created.key }
}

/** 拆分选中的时段：取它的中点 */
export function splitMid(ps: readonly DraftPeriod[], key: string): SplitResult | null {
  const host = ps.find((p) => p.key === key)
  if (!host) return null
  const len = lengthOf(host)
  if (len < 2 || ps.length >= MAX_PERIODS) return null
  return splitAt(ps, (host.start + Math.floor(len / 2)) % DAY)
}

/** 设置时区里的「现在」是当天第几分钟；时区名非法时回退 UTC。与浏览器本地时区无关。 */
export function minuteInZone(ms: number, timeZone: string): number {
  const parts = (zone: string) =>
    new Intl.DateTimeFormat('en-US', { timeZone: zone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(ms))
  let list: Intl.DateTimeFormatPart[]
  try {
    list = parts(timeZone)
  } catch {
    list = parts('UTC')
  }
  const get = (t: string) => Number(list.find((p) => p.type === t)?.value ?? 0)
  return (get('hour') % 24) * 60 + get('minute')
}

export interface NextChange {
  /** 下一次变化发生在当天（或次日）的第几分钟 */
  at: number
  theme: string
  inMinutes: number
}

/**
 * 从 nowMs 起，设置时区里下一次出现「当天第 atMinute 分钟」的真实时刻，距现在多少分钟。
 * 用真实时刻差而不是挂钟分钟差，夏令时切换当天不会差一小时；该挂钟时刻不存在（春季跳变）时取之后的第一个有效时刻。
 */
export function minutesUntilWallMinute(nowMs: number, timeZone: string, atMinute: number): number {
  const wallNow = minuteInZone(nowMs, timeZone)
  let guess = nowMs + (((atMinute - wallNow) % DAY + DAY) % DAY || DAY) * 60_000
  for (let i = 0; i < 4; i++) {
    let diff = atMinute - minuteInZone(guess, timeZone)
    if (diff > DAY / 2) diff -= DAY
    if (diff < -DAY / 2) diff += DAY
    if (diff === 0) break
    guess += diff * 60_000
  }
  return Math.max(1, Math.round((guess - nowMs) / 60_000))
}

/** 当前时段与下一次可见变化（主题或开关屏不同才算，与后端 next_change 同口径）。
 *  传入 clock 时倒计时按真实时刻差计算（识别夏令时），否则按挂钟分钟差。 */
export function currentAndNext(
  ps: readonly DraftPeriod[],
  nowMinute: number,
  clock?: { nowMs: number; timeZone: string },
): { current: DraftPeriod | null; next: NextChange | null } {
  const i = locate(ps, nowMinute)
  if (i < 0) return { current: null, next: null }
  const current = ps[i]
  let at = current.start + lengthOf(current)
  for (let step = 1; step < ps.length; step++) {
    const p = ps[(i + step) % ps.length]
    if (p.theme !== current.theme) {
      const inMinutes = clock ? minutesUntilWallMinute(clock.nowMs, clock.timeZone, at % DAY) : ((at - nowMinute) % DAY + DAY) % DAY || DAY
      return { current, next: { at: at % DAY, theme: p.theme, inMinutes } }
    }
    at += lengthOf(p)
  }
  return { current, next: null }
}

/** 各主题（含 off）当前使用的时间范围，文案形如 07:00–19:00 */
export function themeUsage(ps: readonly DraftPeriod[]): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  for (const p of [...ps].sort(byStart)) (out[p.theme] ??= []).push(`${formatHM(p.start)}–${formatHM(p.end)}`)
  return out
}
