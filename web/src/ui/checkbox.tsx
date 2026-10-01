import { Check } from 'lucide-react'
import { cn } from '@/lib/utils'

interface CheckboxProps {
  checked: boolean
  onChange: (checked: boolean) => void
  // 可访问名称；有可见标签时可通过 labelledBy 关联
  ariaLabel?: string
  id?: string
  disabled?: boolean
  className?: string
}

// 复选框：细线方框，选中时反色并显示对勾
export function Checkbox({ checked, onChange, ariaLabel, id, disabled, className }: CheckboxProps) {
  return (
    <button
      id={id}
      type="button"
      role="checkbox"
      aria-checked={checked}
      aria-label={ariaLabel}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        'grid size-[18px] shrink-0 place-items-center rounded-[2px] border border-line-strong outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50',
        checked ? 'bg-inv-bg text-inv-ink' : 'bg-card',
        className,
      )}
    >
      {checked && <Check size={13} />}
    </button>
  )
}
