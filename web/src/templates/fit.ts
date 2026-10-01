// 文字适配工具：宽度估算、截断、字号自适应、列表「+N」。纯函数，不依赖 DOM。

const LATIN_UNIT = 0.55

function isWide(code: number): boolean {
  return (
    (code >= 0x2e80 && code <= 0x9fff) ||
    (code >= 0xac00 && code <= 0xd7af) ||
    (code >= 0xf900 && code <= 0xfaff) ||
    (code >= 0xff00 && code <= 0xffef)
  )
}

/** 估算文本宽度，单位为 em：中日韩与全角字符计 1，其余计 0.55 */
export function textUnits(text: string): number {
  let u = 0
  for (const ch of text) u += isWide(ch.codePointAt(0) ?? 0) ? 1 : LATIN_UNIT
  return u
}

/** 按宽度预算截断并加省略号；预算不够放一个字符时返回空串 */
export function truncateByWidth(text: string, maxUnits: number): string {
  if (maxUnits <= 0) return ''
  if (textUnits(text) <= maxUnits) return text
  const budget = maxUnits - 1
  let out = ''
  let used = 0
  for (const ch of text) {
    const w = isWide(ch.codePointAt(0) ?? 0) ? 1 : LATIN_UNIT
    if (used + w > budget) break
    out += ch
    used += w
  }
  return out + '…'
}

export interface FitBox {
  width: number
  height: number
  max: number
  min: number
  maxLines?: number
  lineHeight?: number
}

/** 在盒子里放下文本的最大整数字号（px），放不下取 min */
export function fitFontSize(text: string, box: FitBox): number {
  const { width, height, max, min, maxLines = 1, lineHeight = 1.2 } = box
  const units = textUnits(text)
  for (let size = Math.floor(max); size > min; size--) {
    const lines = Math.max(1, Math.ceil((units * size) / width))
    if (lines <= maxLines && lines * size * lineHeight <= height) return size
  }
  return min
}

export interface ListFit {
  shown: number
  more: number
}

/**
 * 列表容量 capacity 行放 total 条。
 * 容量不小于 2 且放不下时，最后一位让给「+N」；容量不超过 1 时至少显示 1 条，不出现孤立的「+N」。
 */
export function fitList(total: number, capacity: number): ListFit {
  if (total <= capacity) return { shown: total, more: 0 }
  if (capacity <= 1) return { shown: 1, more: 0 }
  const shown = capacity - 1
  return { shown, more: total - shown }
}

/** 按高度换算能放几行 */
export function listCapacity(heightPx: number, rowPx: number): number {
  if (rowPx <= 0 || heightPx <= 0) return 0
  return Math.floor(heightPx / rowPx)
}
