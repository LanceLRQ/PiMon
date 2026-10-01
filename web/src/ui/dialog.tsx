import { X } from 'lucide-react'
import type { ComponentProps, ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Dialog as DialogPrimitive } from 'radix-ui'
import { cn } from '@/lib/utils'

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

function Overlay({ className, ...props }: ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn(
        'fixed inset-0 z-40 bg-[rgb(15_15_14/0.45)] data-[state=open]:animate-in data-[state=open]:fade-in-0',
        className,
      )}
      {...props}
    />
  )
}

interface DialogContentProps extends Omit<ComponentProps<typeof DialogPrimitive.Content>, 'title'> {
  title: ReactNode
  description?: ReactNode
  // 标题前的编号框内容（原型里用「!」标危险操作）
  tag?: ReactNode
  tagTone?: 'default' | 'crit'
  footer?: ReactNode
}

// 居中对话框：细线描边的面板，标题栏 + 正文 + 底栏
export function DialogContent({
  title,
  description,
  tag,
  tagTone = 'default',
  footer,
  className,
  children,
  ...props
}: DialogContentProps) {
  const { t } = useTranslation()
  return (
    <DialogPrimitive.Portal>
      <Overlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        {...(description ? {} : { 'aria-describedby': undefined })}
        className={cn(
          'fixed top-1/2 left-1/2 z-50 flex max-h-[85vh] w-[min(520px,calc(100vw-32px))] -translate-x-1/2 -translate-y-1/2 flex-col',
          'rounded-[2px] border border-line-strong bg-card text-card-foreground shadow-[0_20px_50px_rgb(0_0_0/0.25)] outline-none',
          className,
        )}
        {...props}
      >
        <div className="flex h-12 shrink-0 items-center gap-2.5 border-b border-border px-4">
          {tag && (
            <span
              className={cn(
                'rounded-[2px] border px-[5px] font-mono text-xs leading-[18px]',
                tagTone === 'crit' ? 'border-status-crit text-status-crit' : 'border-foreground',
              )}
            >
              {tag}
            </span>
          )}
          <DialogPrimitive.Title className="min-w-0 flex-1 truncate text-[15px] font-medium">{title}</DialogPrimitive.Title>
          <DialogPrimitive.Close
            aria-label={t('common.close')}
            className="grid size-7 place-items-center rounded-[2px] text-muted-foreground outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring"
          >
            <X size={15} />
          </DialogPrimitive.Close>
        </div>
        {description && <DialogPrimitive.Description className="sr-only">{description}</DialogPrimitive.Description>}
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3.5 text-[13px] leading-[1.6]">{children}</div>
        {footer && <div className="flex shrink-0 justify-end gap-2 border-t border-border bg-panel-2 px-4 py-3">{footer}</div>}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}
