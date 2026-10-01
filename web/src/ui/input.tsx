import { Eye, EyeOff } from 'lucide-react'
import { useState, type ComponentProps } from 'react'
import { cn } from '@/lib/utils'

export interface InputProps extends ComponentProps<'input'> {
  // 字段校验失败：红色描边，并标记 aria-invalid
  invalid?: boolean
}

// 细线单行输入框；样式取管理界面 token
export function Input({ className, invalid, ...props }: InputProps) {
  return (
    <input
      aria-invalid={invalid || undefined}
      className={cn(
        'h-8 w-full min-w-0 rounded-[2px] border border-line-strong bg-card px-2.5 text-[13px] outline-none',
        'placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
        invalid && 'border-status-crit shadow-[inset_0_0_0_1px_var(--status-crit)]',
        className,
      )}
      {...props}
    />
  )
}

interface PasswordInputProps extends Omit<InputProps, 'type'> {
  showLabel: string
  hideLabel: string
}

// 带「显示/隐藏」切换的密码框
export function PasswordInput({ showLabel, hideLabel, className, ...props }: PasswordInputProps) {
  const [shown, setShown] = useState(false)
  return (
    <div className="relative">
      <Input type={shown ? 'text' : 'password'} className={cn('pr-9', className)} {...props} />
      <button
        type="button"
        aria-label={shown ? hideLabel : showLabel}
        aria-pressed={shown}
        onClick={() => setShown((v) => !v)}
        className="absolute top-0 right-0 grid h-full w-9 place-items-center text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
      >
        {shown ? <EyeOff size={15} /> : <Eye size={15} />}
      </button>
    </div>
  )
}
