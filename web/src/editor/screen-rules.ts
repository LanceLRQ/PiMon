import type { Layout } from '@/types/generated'

// 与后端 internal/hub/screens/validate.go 对齐：screen 数上限、名称长度（按字符）、id 格式、停留秒数与网格范围
export const MAX_SCREENS = 32
export const MAX_SCREEN_NAME_RUNES = 64
export const MIN_DWELL_SECONDS = 3
export const MAX_DWELL_SECONDS = 3600
export const MAX_GRID_COLS = 12
export const MAX_GRID_ROWS = 8
export const SCREEN_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/

/** 名称规整（后端同样 trim）：返回 null 表示不合法（空或过长） */
export function normalizeScreenName(name: string): string | null {
  const n = name.trim()
  const len = [...n].length
  return len >= 1 && len <= MAX_SCREEN_NAME_RUNES ? n : null
}

/** 下一个未被占用的 screenN（沿用种子布局的命名），超过上限返回 null */
export function nextScreenId(layout: Layout): { id: string; n: number } | null {
  if (layout.screens.length >= MAX_SCREENS) return null
  const used = new Set(layout.screens.map((s) => s.id))
  for (let n = 1; ; n++) if (!used.has(`screen${n}`)) return { id: `screen${n}`, n }
}
