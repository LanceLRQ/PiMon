import { Hourglass } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { TemplateProps } from './types'

// 尚未实现的模板（如 quota、quota-multi，以及后续才做的模板）：中性占位，不报错（Ruling 16）。
export function PendingTemplate({ widget }: TemplateProps) {
  const { t } = useTranslation()
  return (
    <section
      data-widget-frame=""
      data-widget-id={widget.id}
      data-template={widget.template}
      data-template-pending={widget.template}
      className="bg-s-card text-s-muted-fg flex h-full w-full min-w-0 flex-col items-center justify-center gap-1 overflow-hidden text-center text-[length:var(--size-label)]"
      style={{ border: 'var(--state-placeholder-border)', borderRadius: 'var(--radius-card)', padding: 'calc(var(--card-pad) * 0.6)' }}
    >
      <Hourglass size={18} aria-hidden="true" />
      {widget.title && <span className="max-w-full truncate" title={widget.title}>{widget.title}</span>}
      <span>{t('screenWidget.templatePending')}</span>
    </section>
  )
}
