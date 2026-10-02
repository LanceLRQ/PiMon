import { useCallback, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { cn } from '@/lib/utils'
import type { ScreenControlRequest } from '@/types/generated'
import { NumberTag } from '@/ui/numbered-label'
import { useToast } from '@/ui/toast'

/** 总览屏幕卡的「临时亮屏」按键使用的分钟数 */
export const WAKE_MINUTES = 30

/**
 * 发送一条屏幕控制指令：成功弹出「已发送」提示并回调刷新，失败弹出翻译后的错误。
 * 总览屏幕卡与远程操作页共用，保证两处的调用与提示一致。
 */
export function useScreenControl(onDone: () => void | Promise<void>) {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const send = useCallback(
    async (req: ScreenControlRequest): Promise<boolean> => {
      setBusy(true)
      try {
        await http.post('/api/screen/control', req)
        toast.show(t('overview.screen.sent', { action: t(`overview.screen.action.${req.action}`) }))
        void onDone()
        return true
      } catch (e) {
        toast.show(translateErrorValue(i18n, e), 'warn')
        return false
      } finally {
        setBusy(false)
      }
    },
    [i18n, onDone, t, toast],
  )
  return { busy, send }
}

const keyClass = (danger?: boolean) =>
  cn('flex flex-col gap-1 rounded-[2px] border px-2.5 py-2 text-left text-[12px]', danger ? 'border-status-crit' : 'border-line-strong')

interface RemoteKeyFrameProps {
  k: string
  icon: ReactNode
  label: string
  desc?: string
  className?: string
  testId?: string
  disabled?: boolean
  children: ReactNode
}

/** 带自带控件的按键外框（如 k2 的目标选择、k5 的分钟选择）：外观与 RemoteKey 一致，但本身不是按钮 */
export function RemoteKeyFrame({ k, icon, label, desc, className, testId, disabled, children }: RemoteKeyFrameProps) {
  return (
    <div data-testid={testId ?? `key-${k}`} aria-disabled={disabled || undefined} className={cn(keyClass(), disabled && 'opacity-50', className)}>
      <span className="flex items-center justify-between">
        {icon}
        <NumberTag no={k} />
      </span>
      <span>{label}</span>
      {desc && <span className="text-[11.5px] leading-[1.45] text-muted-foreground mobile:hidden">{desc}</span>}
      <div className="mt-auto flex items-center gap-1.5 pt-1">{children}</div>
    </div>
  )
}

interface RemoteKeyProps {
  k: string
  icon: ReactNode
  label: string
  sub?: string
  desc?: string
  disabled?: boolean
  danger?: boolean
  onClick: () => void
  className?: string
  testId?: string
}

/** 远程操作按键：图标 + 编号 + 名称（可带副文案与说明），总览与远程页共用 */
export function RemoteKey({ k, icon, label, sub, desc, disabled, danger, onClick, className, testId }: RemoteKeyProps) {
  return (
    <button
      type="button"
      data-testid={testId ?? `key-${k}`}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        keyClass(danger),
        'outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 disabled:hover:bg-transparent',
        className,
      )}
    >
      <span className={cn('flex items-center justify-between', danger && 'text-status-crit')}>
        {icon}
        <NumberTag no={k} />
      </span>
      <span className={cn(danger && 'text-status-crit')}>{label}</span>
      {sub && <span className="truncate font-mono text-[11px] text-muted-foreground">{sub}</span>}
      {desc && <span className="text-[11.5px] leading-[1.45] text-muted-foreground mobile:hidden">{desc}</span>}
    </button>
  )
}
