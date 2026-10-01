import { useEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'
import { formatHM, parseHM } from './model'

interface Props {
  /** 当前值（当天第几分钟） */
  value: number
  onCommit: (minute: number) => void
  ariaLabel: string
  /** 输入内容不是合法 HH:MM 时的说明，用于 aria-describedby 之外的可见提示 */
  onInvalid?: (invalid: boolean) => void
  className?: string
}

/**
 * HH:MM 输入框：输入到合法时间就立即提交（联动时间轴与列表），不合法时只标红、不改计划；
 * 失焦后回到当前值。外部改动（拖动边界、相邻段联动）在输入框没有焦点时同步显示。
 */
export function TimeField({ value, onCommit, ariaLabel, onInvalid, className }: Props) {
  const ref = useRef<HTMLInputElement>(null)
  const [invalid, setInvalid] = useState(false)

  useEffect(() => {
    const el = ref.current
    if (el && document.activeElement !== el) el.value = formatHM(value)
  }, [value])

  const mark = (v: boolean) => {
    setInvalid(v)
    onInvalid?.(v)
  }

  return (
    <input
      ref={ref}
      type="text"
      inputMode="numeric"
      autoComplete="off"
      aria-label={ariaLabel}
      aria-invalid={invalid || undefined}
      defaultValue={formatHM(value)}
      onChange={(e) => {
        const m = parseHM(e.target.value)
        if (m === null) return mark(true)
        mark(false)
        if (m !== value) onCommit(m)
      }}
      onBlur={(e) => {
        e.target.value = formatHM(value)
        mark(false)
      }}
      className={cn(
        'h-8 w-[76px] rounded-[2px] border border-line-strong bg-card text-center font-mono text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring',
        invalid && 'border-status-crit',
        className,
      )}
    />
  )
}
