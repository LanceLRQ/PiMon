import type { ReactNode } from 'react'

interface PageHeaderProps {
  // 页面编号，如 01；独立页（登录、首次设置）不传
  no?: string
  title: string
  // 标题后的补充说明（桌面显示）
  sub?: string
  // 右侧操作区
  actions?: ReactNode
}

// 页头：编号框 + 标题 + 补充说明 + 操作区，底部 1px 细线
export function PageHeader({ no, title, sub, actions }: PageHeaderProps) {
  return (
    <header className="flex h-14 shrink-0 items-center gap-3.5 border-b border-border bg-card px-6 mobile:h-[52px] mobile:px-3.5">
      <div className="flex items-baseline gap-2.5">
        {no && (
          <span className="rounded-[2px] border border-foreground px-[5px] font-mono text-xs leading-[18px]">{no}</span>
        )}
        <h1 className="text-lg font-medium mobile:text-[17px]">{title}</h1>
        {sub && <span className="text-[13px] text-muted-foreground mobile:hidden">{sub}</span>}
      </div>
      <div className="flex-1" />
      {actions}
    </header>
  )
}

