import { useTranslation } from 'react-i18next'
import type { Item, ResolvedRef, ResolvedWidget, WidgetRef } from '@/types/generated'
import type { StatusLevel } from '@/themes/types'
import { findItem, levelOfValue, parseThreshold, readNumber, resolveValueLevel } from './data'
import { useScreenEnv } from './env'
import { formatNumber } from './format'
import { usePluginText } from './plugin-text'
import type { InstanceDataMap, Threshold } from './types'
import { readItem } from './value'

// 列表与状态格共用：把一个数据项引用换成「名称 + 读数 + 级别」。

export interface ItemView {
  /** 名称；过长由调用方截断，title 用全文 */
  name: string
  /** 读数文字；缺失为 null，由调用方显示「未知」 */
  text: string | null
  unit?: string
  /** 没有级别（中性）为 null */
  level: StatusLevel | null
  item: Item | undefined
}

const bracket = /\[(.*)\]$/

/**
 * 数据项名称：优先 report 里的 label，其次服务端解析的标题（manifest 的 title，动态成员为方括号里的名字），
 * 再其次动态键方括号里的名字（target[Google] → Google），最后用键本身
 */
export function itemName(key: string, label?: string, title?: string): string {
  return label || title || bracket.exec(key)?.[1] || key
}

/** net-reach 的 target[*] 是成功率：100 正常、0 严重、其余警告（成功率本身没有手动阈值时的约定） */
function reachLevel(value: number): StatusLevel {
  return value >= 100 ? 'ok' : value <= 0 ? 'critical' : 'warning'
}

function isReach(item: Item): boolean {
  return item.type === 'gauge' && item.key.startsWith('target[')
}

/** target[X] 对应的延迟项 latency[X]，同一实例内配对 */
export function pairedLatency(ref: WidgetRef, data: InstanceDataMap): Item | undefined {
  const m = /^target\[(.*)\]$/.exec(ref.item)
  if (!m) return undefined
  return findItem({ instance_id: ref.instance_id, item: `latency[${m[1]}]` }, data)
}

export function useItemDescriber(widget: ResolvedWidget, defaultThreshold?: Threshold) {
  const { t } = useTranslation()
  const { lang } = useScreenEnv()
  const pluginText = usePluginText(widget.plugin_id)
  const manual = parseThreshold(widget.options.threshold)
  const levelLabel = (l: StatusLevel) => t(`screenWidget.level.${l}`)

  return {
    pluginText,
    describe(ref: WidgetRef | ResolvedRef, data: InstanceDataMap): ItemView {
      const item = findItem(ref, data)
      const name = itemName(ref.item, item?.label, 'title' in ref ? ref.title : undefined)
      if (!item) return { name, text: null, level: 'unknown', item }
      const reading = readItem(item, ref.field, lang, pluginText, levelLabel)
      let level: StatusLevel | null
      if (item.error) level = 'error'
      else if (!reading) level = 'unknown'
      else if (item.type === 'state') level = levelOfValue(item.state)
      else if (isReach(item)) {
        const v = readNumber(item, ref.field)
        level = v === null ? 'unknown' : reachLevel(v)
      } else level = resolveValueLevel(item, ref.field, manual, defaultThreshold)
      const unit = reading?.unit ?? (isReach(item) && reading ? '%' : undefined)
      return { name, text: reading?.text ?? null, unit, level, item }
    },
    /** 延迟等配对数值的文字，缺失为 null */
    format(item: Item | undefined): string | null {
      if (!item) return null
      const v = readNumber(item)
      return v === null ? null : `${formatNumber(v, lang)}${item.unit ? ` ${item.unit}` : ''}`
    },
  }
}
