import { useState } from 'react'

// 品牌标识（Ruling 28）：本期不入库任何第三方 logo。
// 数据目录 logos/<plugin_id>.svg|png 可覆盖，经 GET /api/logos/{plugin_id} 以 <img> 加载；
// 没有文件或加载失败时回落到字标徽章。徽章只用屏幕端 L2 token（s-* 类）。

/** 取字标：含中日韩字符取首字；多个英文词取各词首字母（最多 3 个）；全大写短词原样；其余取首字母 */
export function abbreviate(name: string): string {
  const text = name.trim()
  if (!text) return '?'
  const first = Array.from(text)[0]
  if (/[぀-ヿ㐀-鿿豈-﫿]/.test(first)) return first
  const words = text.split(/[\s\-_/]+/).filter(Boolean)
  if (words.length > 1) return words.slice(0, 3).map((w) => Array.from(w)[0]).join('').toUpperCase()
  const word = words[0]
  if (word.length <= 3 && word === word.toUpperCase()) return word
  return Array.from(word)[0].toUpperCase()
}

export function brandLogoUrl(pluginId: string): string {
  return `/api/logos/${encodeURIComponent(pluginId)}`
}

// 本页会话内已确认没有 logo 的插件，避免每次渲染都重新请求并再失败
const missing = new Set<string>()

/** 仅测试用：清空失败记录 */
export function resetBrandLogoCache(): void {
  missing.clear()
}

interface BadgeProps {
  name: string
  abbr?: string
  size?: number
  className?: string
}

/** 字标徽章：屏幕 token 的中性方块加缩写 */
export function BrandBadge({ name, abbr, size = 22, className = '' }: BadgeProps) {
  return (
    <span
      data-brand-fallback=""
      role="img"
      title={name}
      aria-label={name}
      style={{ width: size, height: size, fontSize: Math.max(9, Math.round(size * 0.42)), borderRadius: 'calc(var(--radius-card) / 3)' }}
      className={`border-s-border bg-s-muted text-s-muted-fg inline-grid shrink-0 place-items-center border font-[family-name:var(--font-label)] leading-none font-medium ${className}`}
    >
      <span aria-hidden="true">{abbr ?? abbreviate(name)}</span>
    </span>
  )
}

interface MarkProps extends BadgeProps {
  pluginId: string
}

/** 先尝试加载 logo，失败回落字标徽章 */
export function BrandMark({ pluginId, name, abbr, size = 22, className = '' }: MarkProps) {
  const [failedId, setFailedId] = useState<string | null>(null)
  if (failedId === pluginId || missing.has(pluginId)) {
    return <BrandBadge name={name} abbr={abbr} size={size} className={className} />
  }
  return (
    <img
      src={brandLogoUrl(pluginId)}
      alt={name}
      title={name}
      width={size}
      height={size}
      className={`shrink-0 object-contain ${className}`}
      onError={() => {
        missing.add(pluginId)
        setFailedId(pluginId)
      }}
    />
  )
}
