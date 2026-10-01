import { cn } from '@/lib/utils'

// 取字标：含中日韩字符取首字；多个英文词取各词首字母（最多 3 个）；单个短词（≤3 个字母且全大写）原样；
// 其余单词取首字母。
export function abbreviate(name: string): string {
  const text = name.trim()
  if (!text) return '?'
  const first = Array.from(text)[0]
  if (/[぀-ヿ㐀-鿿豈-﫿]/.test(first)) return first
  const words = text.split(/[\s\-_/]+/).filter(Boolean)
  if (words.length > 1) return words.slice(0, 3).map((w) => Array.from(w)[0]).join('').toUpperCase()
  const word = words[0]
  if (word.length <= 3 && word === word.toUpperCase()) return word
  return Array.from(word)[0].toUpperCase()
}

interface WordmarkBadgeProps {
  name: string
  // 显式指定缩写，优先于从 name 推算
  abbr?: string
  size?: number
  className?: string
}

// 字标徽章：第三方品牌一律用中性色方块加缩写（D47），不使用任何品牌 logo
export function WordmarkBadge({ name, abbr, size = 22, className }: WordmarkBadgeProps) {
  const text = abbr ?? abbreviate(name)
  return (
    <span
      title={name}
      aria-label={name}
      role="img"
      style={{ width: size, height: size, fontSize: Math.max(9, Math.round(size * 0.42)) }}
      className={cn(
        'inline-grid shrink-0 place-items-center rounded-[3px] border border-line-strong bg-panel-2 font-mono font-medium leading-none text-ink-2',
        className,
      )}
    >
      <span aria-hidden="true">{text}</span>
    </span>
  )
}
