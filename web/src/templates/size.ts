import type { WidgetSize } from '@/types/generated'

export function sizeKey(size: WidgetSize): string {
  return `${size.cols}x${size.rows}`
}

/** 内容取舍的三档：compact 为 1x1，wide 为单行（2x1、4x1），large 为两行及以上 */
export type LayoutVariant = 'compact' | 'wide' | 'large'

export function layoutVariant(size: WidgetSize): LayoutVariant {
  if (size.cols * size.rows <= 1) return 'compact'
  return size.rows <= 1 ? 'wide' : 'large'
}
