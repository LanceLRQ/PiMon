import { Minus, Plus } from 'lucide-react'
import { cn } from '@/lib/utils'

interface StepperProps {
  value: number
  min: number
  max: number
  // 每次点按的增减量，默认 1
  step?: number
  onChange: (value: number) => void
  // 数值后的单位，如「份」
  unit?: string
  decrementLabel: string
  incrementLabel: string
  ariaLabel: string
  className?: string
}

// 步进器：减、数值加单位、加；到达边界时对应按钮禁用
export function Stepper({ value, min, max, step = 1, onChange, unit, decrementLabel, incrementLabel, ariaLabel, className }: StepperProps) {
  const btn =
    'grid h-8 w-8 shrink-0 place-items-center text-muted-foreground outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40'
  return (
    <div role="group" aria-label={ariaLabel} className={cn('inline-flex w-[150px] items-stretch rounded-[2px] border border-line-strong bg-card', className)}>
      <button type="button" aria-label={decrementLabel} disabled={value <= min} onClick={() => onChange(Math.max(min, value - step))} className={btn}>
        <Minus size={14} />
      </button>
      <div aria-live="polite" className="flex min-w-0 flex-1 items-center justify-center gap-1 border-x border-border text-[13px]">
        <b className="font-mono font-medium">{value}</b>
        {unit}
      </div>
      <button type="button" aria-label={incrementLabel} disabled={value >= max} onClick={() => onChange(Math.min(max, value + step))} className={btn}>
        <Plus size={14} />
      </button>
    </div>
  )
}
