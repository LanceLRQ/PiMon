import type { i18n as I18n } from 'i18next'
import { RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import { cn } from '@/lib/utils'
import { formatLogTime, levelClass, type LogLevel } from '@/pages/system/LogPanel'
import type { Device, LogList } from '@/types/generated'
import { Button } from '@/ui/button'
import { Note } from '@/ui/note'
import { Segmented } from '@/ui/segmented'
import { incompatibleText } from '../model'

const levels: LogLevel[] = ['all', 'info', 'warn', 'error']
const fetchLimit = 200

// 日志场景的错误文案：超时与失败不沿用采集的通用文案
function logErrorText(i18n: I18n, e: unknown): string {
  const t = i18n.t.bind(i18n)
  if (isApiError(e)) {
    if (e.code === 'run.timeout') return t('devices.logs.err.timeout')
    if (e.code === 'run.failed') {
      const m = typeof e.details.message === 'string' ? e.details.message : ''
      return m ? t('common.withDetail', { summary: t('devices.logs.err.failed'), detail: m }) : t('devices.logs.err.failed')
    }
    if (e.code === 'device.offline') return t('devices.logs.err.offline')
    if (e.code === 'device.incompatible') return incompatibleText(i18n, typeof e.details.reason === 'string' ? e.details.reason : null)
  }
  return translateErrorValue(i18n, e)
}

// 日志面板：日志经 agent 通道按需拉取，设备离线时不请求也不轮询；进入、切换级别、点刷新时各取一次。
export function DeviceLogs({ device }: { device: Device }) {
  const { t, i18n } = useTranslation()
  const [level, setLevel] = useState<LogLevel>('all')
  const [list, setList] = useState<LogList | null>(null)
  const [error, setError] = useState<string | null>(null)
  // 只采用最后一次请求的结果，避免快速切换级别时旧响应覆盖新结果
  const seq = useRef(0)
  const available = device.presence === 'online' && !device.incompatible
  const id = device.id

  const load = useCallback(
    async (lv: LogLevel) => {
      const mine = ++seq.current
      try {
        const next = await http.get<LogList>(`/api/devices/${id}/logs`, { query: { level: lv === 'all' ? undefined : lv, limit: fetchLimit } })
        if (mine !== seq.current) return
        setList(next)
        setError(null)
      } catch (e) {
        if (mine !== seq.current) return
        setError(logErrorText(i18n, e))
      }
    },
    [id, i18n],
  )

  useEffect(() => {
    if (!available) return
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入与级别变化时取一次远端日志
    void load(level)
  }, [available, load, level])

  const entries = list ? [...list.entries].reverse() : []
  return (
    <div>
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2">
        <Segmented<LogLevel>
          ariaLabel={t('devices.logs.level')}
          value={level}
          onChange={setLevel}
          disabled={!available}
          options={levels.map((l) => ({ value: l, label: t(`devices.logs.${l}`), title: t(`devices.logs.${l}`) }))}
        />
        <Button size="xs" variant="outline" className="ml-auto rounded-[2px]" disabled={!available} aria-label={t('devices.logs.refresh')} onClick={() => void load(level)}>
          <RefreshCw /> {t('common.refresh')}
        </Button>
      </div>
      {!available ? (
        <div className="px-4 py-3 text-[12.5px] leading-[1.6] text-muted-foreground">
          {device.incompatible ? incompatibleText(i18n, device.incompatible.reason) : t('devices.logs.unavailable')}
        </div>
      ) : error ? (
        <div className="p-3">
          <Note tone="warn" role="alert">{error}</Note>
        </div>
      ) : (
        <div
          role="log"
          tabIndex={0}
          aria-label={t('devices.logs.aria')}
          className="h-[260px] overflow-auto bg-panel-2 py-2 font-mono text-[12px] leading-[1.7] outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {list === null ? (
            <div className="px-4 text-muted-foreground">{t('common.loading')}</div>
          ) : entries.length === 0 ? (
            <div className="px-4 text-muted-foreground">{level === 'all' ? t('devices.logs.emptyAll') : t('devices.logs.empty')}</div>
          ) : (
            entries.map((e, i) => (
              <div key={`${e.time}-${i}`} className="grid grid-cols-[88px_48px_minmax(0,1fr)] gap-2 px-4 whitespace-nowrap hover:bg-card">
                <span className="text-muted-foreground">{formatLogTime(e.time, i18n.language)}</span>
                <span className={cn('font-medium uppercase', levelClass[e.level])}>{e.level}</span>
                <span className="overflow-hidden text-ellipsis" title={e.attrs ? `${e.message} ${e.attrs}` : e.message}>
                  {e.message}
                  {e.attrs && <span className="ml-2 text-muted-foreground">{e.attrs}</span>}
                </span>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}
