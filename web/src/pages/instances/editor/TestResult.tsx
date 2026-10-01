import { ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatDateTime } from '@/lib/time'
import type { PluginOutput, Report } from '@/types/generated'
import { Note } from '@/ui/note'
import { ReportItemView } from '@/ui/report-items'
import { StatusLabel, StatusShape } from '@/ui/status-shape'
import { outputTitle } from '../InstanceDetailDrawer'
import type { TestOutcome } from './types'

interface TestResultProps {
  outcome: TestOutcome | null
  outputs: PluginOutput[]
}

// 插件私有状态可能缓存登录态，原始报告里不展示
function displayReport(report: Report): Report {
  return { ...report, state: undefined }
}

// 保存区下方的测试结果：状态与耗时、数据项、折叠的原始报告 JSON，失败时显示错误信息
export function TestResult({ outcome, outputs }: TestResultProps) {
  const { t, i18n } = useTranslation()
  if (!outcome) {
    return (
      <p className="mx-4 mt-3.5 rounded-[2px] border border-dashed border-line-strong p-3.5 text-[12.5px] leading-[1.6] text-muted-foreground">
        {t('editor.result.empty')}
      </p>
    )
  }
  if (outcome.phase === 'running') {
    return (
      <p role="status" className="mx-4 mt-3.5 text-[13px] text-muted-foreground">
        {t('editor.result.running')}
      </p>
    )
  }
  const time = formatDateTime(outcome.at, i18n.language)
  if (outcome.phase === 'failed') {
    return (
      <div role="alert" className="pb-4">
        <div className="flex items-center gap-2.5 p-4">
          <StatusShape state="error" size={16} />
          <span className="text-[17px] font-medium text-status-crit">{t('status.error')}</span>
          <span className="ml-auto text-right font-mono text-[11.5px] text-muted-foreground">{time}</span>
        </div>
        <pre className="mx-4 mb-3 max-h-48 overflow-auto rounded-[2px] border border-status-crit bg-card p-2.5 font-mono text-[12px] leading-[1.55] break-words whitespace-pre-wrap text-status-crit">
          {outcome.message}
        </pre>
        <p className="mx-4 text-[12px] leading-[1.55] text-muted-foreground">
          {outcome.saved ? t('editor.result.failedSaved') : t('editor.result.failedNotSaved')}
        </p>
      </div>
    )
  }
  const { instance, report } = outcome
  const items = report?.items ?? []
  const ms = report?.duration_ms ?? outcome.wallMs
  const raw = report ? JSON.stringify(displayReport(report), null, 2) : ''
  return (
    <div className="pb-2">
      <div className="flex items-center gap-2.5 p-4">
        <span className="text-[17px] font-medium">
          <StatusLabel state={instance.display_state} />
        </span>
        <span className="ml-auto text-right font-mono text-[11.5px] leading-[1.5] text-muted-foreground">
          {t('editor.result.duration', { ms })}
          <br />
          {t('editor.result.at', { time })}
        </span>
      </div>
      {report?.summary && <p className="px-4 pb-3 text-[13px]">{report.summary}</p>}
      {report?.stale && (
        <div className="px-4 pb-3">
          <Note tone="warn" role="status">
            {t('editor.result.stale')}
          </Note>
        </div>
      )}
      {instance.last_error && (
        <pre className="mx-4 mb-3 max-h-40 overflow-auto rounded-[2px] border border-status-crit p-2.5 font-mono text-[12px] leading-[1.55] break-words whitespace-pre-wrap text-status-crit">
          {instance.last_error}
        </pre>
      )}
      <div className="border-y border-border">
        <div className="px-4 pt-2 font-mono text-[11px] text-muted-foreground">{t('editor.result.items', { count: items.length })}</div>
        {items.length === 0 && <p className="px-4 py-2 text-[12.5px] text-muted-foreground">{t('editor.result.noItems')}</p>}
        {items.map((it) => (
          <ReportItemView key={it.key} item={it} title={outputTitle(outputs, it.key)} now={outcome.at} />
        ))}
      </div>
      {report && (
        <details className="group border-b border-border">
          <summary className="flex h-9 cursor-pointer list-none items-center gap-2 px-4 text-[12.5px] text-ink-2 [&::-webkit-details-marker]:hidden">
            <ChevronRight size={14} aria-hidden className="transition-transform group-open:rotate-90" />
            {t('editor.result.rawJson')}
            <span className="ml-auto font-mono text-[11px] text-muted-foreground">{t('editor.result.rawSize', { size: raw.length })}</span>
          </summary>
          <pre className="mx-4 mb-3.5 max-h-64 overflow-auto rounded-[2px] border border-border bg-panel-2 p-2.5 font-mono text-[11.5px] leading-[1.55] text-ink-2">
            {raw}
          </pre>
        </details>
      )}
    </div>
  )
}
