import { useTranslation } from 'react-i18next'
import { CopyButton } from '@/ui/copy-button'

interface CommandBlockProps {
  title: string
  text: string
}

// 命令区：标题 + 一键复制；命令横向滚动、不折行（手机上也一样），可键盘聚焦后滚动
export function CommandBlock({ title, text }: CommandBlockProps) {
  const { t } = useTranslation()
  return (
    <div className="mb-2.5 min-w-0 rounded-[2px] border border-line-strong bg-panel-2">
      <div className="flex items-center justify-between gap-2 border-b border-border px-2.5 py-1 text-[12px] text-muted-foreground">
        <span className="min-w-0 truncate">{title}</span>
        <CopyButton text={text} label={t('devices.add.copyCommand')} />
      </div>
      <pre tabIndex={0} className="m-0 overflow-x-auto px-2.5 py-2 font-mono text-[12px] leading-[1.6] whitespace-pre">{text}</pre>
    </div>
  )
}
