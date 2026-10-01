import { RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { cn } from '@/lib/utils'
import type { LogList } from '@/types/generated'
import { Button } from '@/ui/button'
import { Note } from '@/ui/note'
import { Section } from '@/ui/section'
import { Segmented } from '@/ui/segmented'

export type LogLevel = 'all' | 'info' | 'warn' | 'error'
const levels: LogLevel[] = ['all', 'info', 'warn', 'error']

// 中枢内存日志最多 500 条，页面一次取满
const fetchLimit = 500

function formatLogTime(iso: string, locale: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleTimeString(locale, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit', fractionalSecondDigits: 3 })
}

const levelClass: Record<string, string> = {
  debug: 'text-muted-foreground',
  info: 'text-ink-2',
  warn: 'text-status-warn',
  error: 'text-status-crit',
}

// 最近 hub 日志：级别筛选交给服务端（level 参数），默认每 10 秒刷新，也可手动刷新
export function LogPanel({ refreshMs = 10_000 }: { refreshMs?: number }) {
  const { t, i18n } = useTranslation()
  const [level, setLevel] = useState<LogLevel>('all')
  const [list, setList] = useState<LogList | null>(null)
  const [error, setError] = useState<string | null>(null)
  // 只采用最后一次请求的结果，避免快速切换级别时旧响应覆盖新结果
  const seq = useRef(0)

  const load = useCallback(
    async (lv: LogLevel) => {
      const mine = ++seq.current
      try {
        const next = await http.get<LogList>('/api/system/logs', { query: { level: lv === 'all' ? undefined : lv, limit: fetchLimit } })
        if (mine !== seq.current) return
        setList(next)
        setError(null)
      } catch (e) {
        if (mine !== seq.current) return
        setError(translateErrorValue(i18n, e))
      }
    },
    [i18n],
  )

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 级别变化与进入页面时取远端日志
    void load(level)
    const id = setInterval(() => void load(level), refreshMs)
    return () => clearInterval(id)
  }, [load, level, refreshMs])

  // 新的在上
  const entries = list ? [...list.entries].reverse() : []
  return (
    <Section
      no="06.6"
      title={t('system.logs.title')}
      meta={list ? t('system.logs.meta', { count: list.entries.length, capacity: list.capacity }) : undefined}
    >
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2">
        <Segmented<LogLevel>
          ariaLabel={t('system.logs.level')}
          value={level}
          onChange={setLevel}
          options={levels.map((l) => ({ value: l, label: t(`system.logs.${l}`), title: t(`system.logs.${l}`) }))}
        />
        <span className="min-w-0 flex-1 text-[11.5px] text-muted-foreground mobile:hidden">{t('system.logs.hint')}</span>
        <Button size="xs" variant="outline" className="ml-auto rounded-[2px]" aria-label={t('system.logs.refresh')} onClick={() => void load(level)}>
          <RefreshCw /> {t('common.refresh')}
        </Button>
      </div>
      {error ? (
        <div className="p-3">
          <Note tone="crit" role="alert">
            {t('common.withDetail', { summary: t('system.logs.loadFailed'), detail: error })}
          </Note>
        </div>
      ) : (
        <div
          role="log"
          tabIndex={0}
          aria-label={t('system.logs.aria')}
          className="h-[340px] overflow-auto bg-panel-2 py-2 font-mono text-[12px] leading-[1.7] outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {list === null ? (
            <div className="px-4 text-muted-foreground">{t('common.loading')}</div>
          ) : entries.length === 0 ? (
            <div className="px-4 text-muted-foreground">{level === 'all' ? t('system.logs.emptyAll') : t('system.logs.empty')}</div>
          ) : (
            entries.map((e, i) => (
              <div key={`${e.time}-${i}`} className="grid grid-cols-[96px_52px_minmax(0,1fr)] gap-2.5 px-4 whitespace-nowrap hover:bg-card mobile:grid-cols-[84px_44px_minmax(0,1fr)]">
                <span className="text-muted-foreground">{formatLogTime(e.time, i18n.language)}</span>
                <span className={cn('font-medium uppercase', levelClass[e.level])}>{e.level}</span>
                <span className="overflow-hidden text-ellipsis" title={e.attrs ? `${e.message} ${e.attrs}` : e.message}>
                  {e.message}
                  {e.attrs && <span className="ml-2 text-muted-foreground mobile:hidden">{e.attrs}</span>}
                </span>
              </div>
            ))
          )}
        </div>
      )}
    </Section>
  )
}
