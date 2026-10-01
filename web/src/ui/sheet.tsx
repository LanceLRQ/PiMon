import { X } from 'lucide-react'
import type { ComponentProps, ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Dialog as DialogPrimitive } from 'radix-ui'
import { cn } from '@/lib/utils'

export const Sheet = DialogPrimitive.Root

interface SheetContentProps extends Omit<ComponentProps<typeof DialogPrimitive.Content>, 'title'> {
  // 头部：标题区（图标、名称等），可访问名称取自 srTitle
  header: ReactNode
  srTitle: string
  footer?: ReactNode
}

// 右侧抽屉：桌面 480px 宽，窄屏占满；正文自己滚动
export function SheetContent({ header, srTitle, footer, className, children, ...props }: SheetContentProps) {
  const { t } = useTranslation()
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay
        data-slot="sheet-overlay"
        className="fixed inset-0 z-40 bg-[rgb(15_15_14/0.45)] data-[state=open]:animate-in data-[state=open]:fade-in-0"
      />
      <DialogPrimitive.Content
        data-slot="sheet-content"
        aria-describedby={undefined}
        className={cn(
          'fixed inset-y-0 right-0 z-50 flex w-[480px] max-w-full flex-col border-l border-line-strong bg-card text-card-foreground',
          'shadow-[-20px_0_50px_rgb(0_0_0/0.2)] outline-none data-[state=open]:animate-in data-[state=open]:slide-in-from-right',
          className,
        )}
        {...props}
      >
        <DialogPrimitive.Title className="sr-only">{srTitle}</DialogPrimitive.Title>
        <div className="flex min-h-[52px] shrink-0 items-center gap-2.5 border-b border-border px-4 py-2">
          <div className="min-w-0 flex-1">{header}</div>
          <DialogPrimitive.Close
            aria-label={t('common.close')}
            className="grid size-8 shrink-0 place-items-center rounded-[2px] text-muted-foreground outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring"
          >
            <X size={15} />
          </DialogPrimitive.Close>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
        {footer && <div className="flex shrink-0 flex-wrap justify-end gap-2 border-t border-border bg-panel-2 px-4 py-3">{footer}</div>}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}
