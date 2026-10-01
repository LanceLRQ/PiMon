import { cn } from '@/lib/utils'

interface SwitchProps {
  checked: boolean
  onChange: (checked: boolean) => void
  // 无可见文字标签时必须提供
  ariaLabel?: string
  id?: string
  disabled?: boolean
  className?: string
}

// 开关：细线轨道，打开时轨道反色
export function Switch({ checked, onChange, ariaLabel, id, disabled, className }: SwitchProps) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={ariaLabel}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative inline-block h-[18px] w-8 shrink-0 rounded-full border border-line-strong outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50',
        checked ? 'bg-inv-bg' : 'bg-panel-2',
        className,
      )}
    >
      <span
        aria-hidden
        className={cn(
          'absolute top-px left-px size-3.5 rounded-full transition-transform',
          checked ? 'translate-x-3.5 bg-inv-ink' : 'bg-line-strong',
        )}
      />
    </button>
  )
}
