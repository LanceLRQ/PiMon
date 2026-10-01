import { Play, Save } from 'lucide-react'
import type { Ref } from 'react'
import { useTranslation } from 'react-i18next'
import type { PluginInfo } from '@/types/generated'
import { Button } from '@/ui/button'
import { NumberTag } from '@/ui/numbered-label'
import { SpecList } from '@/ui/spec-list'
import { StatusShape } from '@/ui/status-shape'
import { TestResult } from './TestResult'
import type { TestOutcome } from './types'

interface SavePanelProps {
  plugin: PluginInfo | null
  name: string
  intervalLabel: string
  errorCount: number
  busy: 'save' | 'test' | null
  disabled: boolean
  outcome: TestOutcome | null
  resultRef?: Ref<HTMLDivElement>
  onSave: () => void
  onSaveTest: () => void
}

// 保存区：配置汇总、「保存」与「保存并测试」、测试结果
export function SavePanel({ plugin, name, intervalLabel, errorCount, busy, disabled, outcome, resultRef, onSave, onSaveTest }: SavePanelProps) {
  const { t } = useTranslation()
  const pass = errorCount === 0
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      <div className="px-4 py-2.5">
        <SpecList
          rows={[
            { label: t('editor.save.summaryPlugin'), value: plugin ? `${plugin.id} v${plugin.version} · ${plugin.origin}` : '—' },
            { label: t('editor.save.summaryName'), value: <span className="break-all">{name.trim() || '—'}</span> },
            { label: t('editor.save.summaryRuns'), value: t('editor.config.runsHub') },
            { label: t('editor.save.summaryInterval'), value: plugin ? intervalLabel : '—' },
            {
              label: t('editor.save.summaryCheck'),
              value: plugin ? (
                <span className="inline-flex items-center gap-1.5">
                  <StatusShape state={pass ? 'ok' : 'critical'} size={12} />
                  {pass ? t('editor.save.checkPass') : t('editor.save.checkFail', { count: errorCount })}
                </span>
              ) : (
                '—'
              ),
              highlight: plugin !== null && !pass,
            },
          ]}
        />
      </div>
      <div className="grid grid-cols-[1fr_1.4fr] gap-2 border-b border-border px-4 pt-1 pb-3.5">
        <Button type="button" variant="outline" className="h-9 rounded-[2px]" disabled={disabled || busy !== null} onClick={onSave}>
          <Save /> {busy === 'save' ? t('editor.save.saving') : t('editor.save.save')}
        </Button>
        <Button type="button" className="h-9 rounded-[2px]" disabled={disabled || busy !== null} onClick={onSaveTest}>
          <Play /> {busy === 'test' ? t('editor.save.testing') : t('editor.save.saveTest')}
        </Button>
      </div>
      <div ref={resultRef} className="flex h-11 shrink-0 scroll-mt-14 items-center gap-2 border-b border-border px-4">
        <NumberTag no="03.1" />
        <h3 className="text-[13.5px] font-medium">{t('editor.result.title')}</h3>
      </div>
      <div aria-live="polite">
        <TestResult outcome={outcome} outputs={plugin?.outputs ?? []} />
      </div>
    </div>
  )
}
