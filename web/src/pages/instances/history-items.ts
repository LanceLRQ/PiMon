import type { Item } from '@/types/generated'

// 有数值历史的数据项类型（与中枢记录历史的类型一致）
const numericTypes = ['gauge', 'number', 'quota', 'money']

export function historyItems(items: Item[] | undefined): Item[] {
  return (items ?? []).filter((i) => numericTypes.includes(i.type))
}
