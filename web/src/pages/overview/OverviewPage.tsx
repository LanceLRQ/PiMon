import { ArrowLeftRight, Plus, Power, RefreshCw, Sun } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { http } from '@/api/client'
import { formatAgo, formatDateTime, parseTime, useNow } from '@/lib/time'
import { cn } from '@/lib/utils'
import { selectConnected, selectInstances, useLiveStore, type LiveState } from '@/store/live-store'
import { formatUptime } from '@/lib/format'
import { useUptimeSeconds } from '@/lib/use-uptime'
import type { BackupInfo, Instance, SystemInfo } from '@/types/generated'
import { useInstanceManager } from '@/pages/instances/actions'
import { InstanceDetailDrawer } from '@/pages/instances/InstanceDetailDrawer'
import { InstanceTable } from '@/pages/instances/InstanceTable'
import { attentionStates, countStates, isAttention, statusRank, type StateCounts } from '@/pages/instances/query'
import { usePlugins } from '@/pages/instances/use-plugins'
import { Button } from '@/ui/button'
import { NumberTag } from '@/ui/numbered-label'
import { PageHeader } from '@/ui/page-header'
import { shapeStates, StatusShape } from '@/ui/status-shape'

const selectSynced = (s: LiveState) => s.synced
const selectBuild = (s: LiveState) => s.build
const selectSettings = (s: LiveState) => s.settings

// 分布条每格的底色（色通道），形状通道由图例与 title 给出
const segColor: Record<string, string> = {
  ok: 'bg-status-ok',
  warning: 'bg-status-warn',
  critical: 'bg-status-crit',
  error: 'bg-status-crit/60',
  stale: 'bg-status-unknown/60',
  broken: 'bg-status-unknown/60',
  offline: 'bg-status-unknown/60',
  unconfigured: 'bg-status-unknown/60',
}

function Cell({ no, title, meta, children, className }: { no: string; title: string; meta?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <section className={cn('min-w-0 bg-card', className)} aria-label={title}>
      <div className="flex h-10 items-center gap-2.5 border-b border-border px-4">
        <NumberTag no={no} />
        <h2 className="text-[14px] font-medium">{title}</h2>
        <span className="ml-auto min-w-0 truncate text-[12px] text-muted-foreground">{meta}</span>
      </div>
      {children}
    </section>
  )
}

type Verdict = { state: string; key: string; count: number; instance: Instance } | null

// 健康结论取最严重的一类，口径同「需要处理」（attentionStates 顺序，不含已暂停）；其后才是其他（未知、维护中）
function verdictOf(list: Instance[], counts: StateCounts): Verdict {
  for (const state of attentionStates) {
    const count = counts[state]
    if (count > 0) {
      const inst = list.filter((i) => !i.paused && i.display_state === state).sort((a, b) => a.name.localeCompare(b.name))[0]
      return { state, key: state, count, instance: inst }
    }
  }
  if (counts.other > 0) {
    const inst = list.find((i) => !i.paused && !['ok', ...attentionStates].includes(i.display_state))
    if (inst) return { state: 'unknown', key: 'other', count: counts.other, instance: inst }
  }
  return null
}

function HealthSummary({ instances, synced }: { instances: Instance[]; synced: boolean }) {
  const { t } = useTranslation()
  const counts = useMemo(() => countStates(instances), [instances])
  const verdict = useMemo(() => verdictOf(instances, counts), [instances, counts])
  const others = ([...attentionStates, 'paused', 'other'] as const).filter((k) => k !== verdict?.key && counts[k] > 0)
  const segState = (i: Instance) => (i.paused ? 'unknown' : i.display_state)
  const segs = useMemo(() => [...instances].sort((a, b) => statusRank(segState(a)) - statusRank(segState(b))), [instances])

  const rows: { state: string; label: string; n: number }[] = [
    { state: 'ok', label: t('status.ok'), n: counts.ok },
    { state: 'warning', label: t('status.warning'), n: counts.warning },
    { state: 'critical', label: t('status.critical'), n: counts.critical },
    { state: 'error', label: t('status.error'), n: counts.error },
    { state: 'stale', label: t('status.stale'), n: counts.stale },
    { state: 'broken', label: t('status.broken'), n: counts.broken },
    { state: 'offline', label: t('status.offline'), n: counts.offline },
    { state: 'unconfigured', label: t('status.unconfigured'), n: counts.unconfigured },
    { state: 'unknown', label: t('overview.pausedState'), n: counts.paused },
    { state: 'unknown', label: t('overview.otherState'), n: counts.other },
  ]

  return (
    <>
      <div className="flex flex-wrap items-center gap-x-8 gap-y-3 px-4 py-4">
        <div>
          <div className="font-mono text-[44px] leading-none tabular-nums">{synced ? counts.total : '—'}</div>
          <div className="mt-1 text-xs text-muted-foreground">{t('overview.instanceUnit')}</div>
        </div>
        <div className="min-w-[220px] flex-1">
          {!synced ? (
            <div className="text-[13px] text-muted-foreground">{t('shell.loading')}</div>
          ) : counts.total === 0 ? (
            <div className="text-[13px] text-muted-foreground">{t('overview.noInstances')}</div>
          ) : verdict ? (
            <div>
              <div className={cn('flex items-start gap-2 text-[14px]', verdict.state === 'critical' && 'font-semibold')}>
                <StatusShape state={verdict.state} size={16} className="mt-0.5" />
                <span className="line-clamp-2 break-words">
                  {t(`overview.verdict.${verdict.key}`, {
                    count: verdict.count,
                    name: verdict.instance.name,
                    reading: verdict.instance.summary || verdict.instance.last_error || '',
                  })}
                </span>
              </div>
              {others.length > 0 && (
                <div className="mt-1 pl-6 text-[12px] text-muted-foreground">
                  {t('overview.also', { list: others.map((k) => t(`overview.alsoItem.${k}`, { count: counts[k] })).join(t('overview.listSep')) })}
                </div>
              )}
            </div>
          ) : counts.paused === counts.total ? (
            <div className="flex items-center gap-2 text-[14px]">
              <StatusShape state="unknown" size={16} />
              <span>{t('overview.verdict.noActive', { count: counts.paused })}</span>
            </div>
          ) : (
            <div className="flex items-center gap-2 text-[14px]">
              <StatusShape state="ok" size={16} />
              <span>{t('overview.verdict.allOk')}</span>
            </div>
          )}
          <dl className="mt-3 grid grid-cols-3 gap-x-5 gap-y-1.5 mobile:grid-cols-2">
            {rows.map((r) => (
              <div key={r.state + r.label} className={cn('flex items-center justify-between gap-2 text-[12.5px]', r.n === 0 && 'text-muted-foreground')}>
                <dt className="flex items-center gap-1.5">
                  <StatusShape state={r.state} size={12} />
                  {r.label}
                </dt>
                <dd className="font-mono tabular-nums">{synced ? r.n : '—'}</dd>
              </div>
            ))}
          </dl>
        </div>
      </div>
      <div className="px-4 pb-3">
        <div role="img" aria-label={t('overview.distribution', { count: counts.total })} className="flex h-2.5 gap-px overflow-hidden rounded-[1px]">
          {segs.map((i) => (
            <span key={i.id} title={`${i.name} · ${i.paused ? t('overview.pausedState') : t(`status.${i.display_state}`, { defaultValue: t('status.unknown') })}`} className={cn('h-full min-w-[3px] flex-1', segColor[segState(i)] ?? 'bg-status-unknown/40')} />
          ))}
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1 border-t border-border px-4 py-2 text-[12px] text-muted-foreground">
        <span className="font-mono text-[10.5px]">{t('overview.legend')}</span>
        {shapeStates.map((s) => (
          <span key={s} className="inline-flex items-center gap-1">
            <StatusShape state={s} size={12} />
            {t(`status.${s}`)}
          </span>
        ))}
      </div>
    </>
  )
}

function HubSummary({ instanceCount }: { instanceCount: number }) {
  const { t, i18n } = useTranslation()
  const build = useLiveStore(selectBuild)
  const connected = useLiveStore(selectConnected)
  const settings = useLiveStore(selectSettings)
  // undefined 表示还没取到；null 表示取到了但没有备份
  const [lastBackup, setLastBackup] = useState<number | null | undefined>(undefined)
  // 中枢已运行秒数（取自接口）；取不到时为 undefined，显示未知
  const [uptimeBase, setUptimeBase] = useState<number | undefined>(undefined)
  const uptime = useUptimeSeconds(uptimeBase, 10_000)

  useEffect(() => {
    let cancelled = false
    http
      .get<SystemInfo>('/api/system')
      .then((info) => {
        if (!cancelled) setUptimeBase(typeof info.uptime_seconds === 'number' ? info.uptime_seconds : undefined)
      })
      .catch(() => {
        if (!cancelled) setUptimeBase(undefined)
      })
    http
      .get<BackupInfo[]>('/api/backups')
      .then((list) => {
        if (cancelled) return
        const times = list.map((b) => parseTime(b.created_at)).filter((x): x is number => x !== null)
        setLastBackup(times.length ? Math.max(...times) : null)
      })
      .catch(() => {
        if (!cancelled) setLastBackup(undefined)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const unknown = <span className="text-muted-foreground">{t('items.unknown')}</span>
  const rows: { k: string; v: React.ReactNode }[] = [
    { k: t('overview.hub.version'), v: build && build !== '__PIMON_BUILD__' ? build : unknown },
    { k: t('overview.hub.uptime'), v: uptime === null ? unknown : formatUptime(t, uptime) },
    {
      k: t('overview.hub.connection'),
      v: (
        <span className="inline-flex items-center gap-1.5 text-[15px]">
          <StatusShape state={connected ? 'ok' : 'offline'} size={14} />
          {connected ? t('overview.hub.online') : t('overview.hub.offline')}
        </span>
      ),
    },
    {
      k: t('overview.hub.lastBackup'),
      v: lastBackup === undefined ? unknown : lastBackup === null ? t('overview.hub.noBackup') : formatDateTime(lastBackup, i18n.language),
    },
    { k: t('overview.hub.timezone'), v: settings?.timezone || unknown },
    { k: t('overview.hub.instances'), v: String(instanceCount) },
    { k: t('overview.hub.agents'), v: <span className="text-muted-foreground">{t('overview.hub.agentsLater')}</span> },
  ]
  return (
    <dl className="grid grid-cols-2 gap-px bg-border">
      {rows.map((r, i) => (
        // 项数为奇数时最后一格撑满整行，避免露出分隔线底色
        <div key={r.k} className={cn('bg-card px-4 py-3', i === rows.length - 1 && rows.length % 2 === 1 && 'col-span-2')}>
          <dt className="text-xs text-muted-foreground">{r.k}</dt>
          <dd className="mt-0.5 font-mono text-[17px] break-words">{r.v}</dd>
        </div>
      ))}
    </dl>
  )
}

function Attention({ instances, now, onOpen, onRetry, synced }: { instances: Instance[]; now: number; onOpen(i: Instance): void; onRetry(i: Instance): void; synced: boolean }) {
  const { t } = useTranslation()
  const list = useMemo(
    () =>
      instances
        .filter((i) => isAttention(i))
        .sort((a, b) => statusRank(b.display_state) - statusRank(a.display_state) || a.name.localeCompare(b.name)),
    [instances],
  )
  if (!synced) return <p className="px-4 py-6 text-center text-[13px] text-muted-foreground">{t('shell.loading')}</p>
  if (list.length === 0) {
    return (
      <p className="flex items-center justify-center gap-2 px-4 py-8 text-[13px] text-muted-foreground">
        <StatusShape state="ok" size={14} />
        {t('overview.attention.none')}
      </p>
    )
  }
  return (
    <ul className="m-0 max-h-[340px] list-none overflow-y-auto p-0">
      {list.map((i) => (
        <li
          key={i.id}
          data-state-row={i.display_state}
          className={cn('grid grid-cols-[auto_1fr_auto] items-center gap-3 border-t border-border px-4 py-2.5 first:border-t-0', i.display_state === 'critical' && 'shadow-[inset_2px_0_0_var(--status-crit)]')}
        >
          <span className="inline-flex w-[72px] items-center gap-1.5 text-[12.5px]">
            <StatusShape state={i.display_state} />
            <span className={cn(i.display_state === 'critical' && 'font-semibold')}>{t(`status.${i.display_state}`)}</span>
          </span>
          <div className="min-w-0">
            <div className="truncate text-[13px]">
              <code className="font-mono text-[12px]">{i.name}</code>
              {(i.summary || i.last_error) && <span className="ml-1.5 text-ink-2">{i.summary || i.last_error}</span>}
            </div>
            <div className="truncate text-[12px] text-muted-foreground">
              {i.plugin_id} · {t('overview.attention.lastSuccess', { ago: formatAgo(t, now, parseTime(i.last_success_at)) })}
            </div>
          </div>
          <div className="flex gap-1.5">
            {i.display_state === 'error' && (
              <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => onRetry(i)}>
                <RefreshCw /> {t('overview.attention.retry')}
              </Button>
            )}
            <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => onOpen(i)}>
              {t('overview.attention.view')}
            </Button>
          </div>
        </li>
      ))}
    </ul>
  )
}

// 屏幕卡片：缩略图与远程按键等屏幕模块（M1d）接入后替换，本期只占位
function ScreenCard() {
  const { t } = useTranslation()
  const keys = [
    { icon: <RefreshCw size={15} />, label: t('overview.screen.keyRefresh'), k: 'k1' },
    { icon: <ArrowLeftRight size={15} />, label: t('overview.screen.keySwitch'), k: 'k2' },
    { icon: <Power size={15} />, label: t('overview.screen.keyOff'), k: 'k3' },
    { icon: <Sun size={15} />, label: t('overview.screen.keyWake'), k: 'k4' },
  ]
  return (
    <>
      <div className="px-4 pt-4">
        <div className="grid h-[120px] place-items-center rounded-[2px] border border-dashed border-line-strong bg-panel-2/50 text-[13px] text-muted-foreground">
          {t('overview.screen.pending')}
        </div>
      </div>
      <div className="grid grid-cols-4 gap-2 px-4 py-3 mobile:grid-cols-2">
        {keys.map((k) => (
          <button
            key={k.k}
            type="button"
            disabled
            title={t('overview.screen.pending')}
            className="flex flex-col gap-1 rounded-[2px] border border-line-strong px-2.5 py-2 text-left text-[12px] opacity-50"
          >
            <span className="flex items-center justify-between">
              {k.icon}
              <NumberTag no={k.k} />
            </span>
            <span>{k.label}</span>
          </button>
        ))}
      </div>
    </>
  )
}

function Readout() {
  const { t, i18n } = useTranslation()
  const now = useNow(10_000)
  const connected = useLiveStore(selectConnected)
  const time = new Date(now).toLocaleString(i18n.language, { weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: false })
  return (
    <div className="flex items-center gap-4 font-mono text-[12px] text-muted-foreground mobile:hidden">
      <span>{time}</span>
      <span className="inline-flex items-center gap-1.5">
        <StatusShape state={connected ? 'ok' : 'offline'} size={12} />
        {connected ? t('overview.hub.online') : t('overview.hub.offline')}
      </span>
    </div>
  )
}

export function OverviewPage() {
  const { t } = useTranslation()
  const instances = useLiveStore(selectInstances)
  const synced = useLiveStore(selectSynced)
  const now = useNow()
  const plugins = usePlugins()
  const [openId, setOpenId] = useState<string | null>(null)
  const manager = useInstanceManager(plugins.list?.plugins)
  const closeDrawer = useCallback(() => setOpenId(null), [])
  const opened = openId ? instances.find((i) => i.id === openId) : undefined
  const openedPlugin = opened ? plugins.list?.plugins.find((p) => p.id === opened.plugin_id) : undefined
  const attentionCount = instances.filter((i) => isAttention(i)).length

  return (
    <>
      <PageHeader no="01" title={t('pages.overview')} sub={t('overview.sub')} actions={<Readout />} />
      <div className="flex flex-col gap-4 p-6 mobile:p-3.5">
        <div className="grid grid-cols-2 gap-px overflow-hidden rounded-[2px] border border-line-strong bg-line-strong mobile:grid-cols-1">
          <Cell no="01.1" title={t('overview.health.title')}>
            <HealthSummary instances={instances} synced={synced} />
          </Cell>
          <Cell no="01.2" title={t('overview.hub.title')} meta={window.location.host} className="mobile:hidden">
            <HubSummary instanceCount={instances.length} />
          </Cell>
          <Cell no="01.3" title={t('overview.attention.title')} meta={synced ? t('overview.attention.meta', { count: attentionCount }) : undefined}>
            <Attention instances={instances} now={now} synced={synced} onOpen={(i) => setOpenId(i.id)} onRetry={(i) => void manager.actions.run(i)} />
          </Cell>
          <Cell no="01.4" title={t('overview.screen.title')} meta={t('overview.screen.meta')}>
            <ScreenCard />
          </Cell>
        </div>

        <InstanceTable
          instances={instances}
          synced={synced}
          now={now}
          actions={manager.actions}
          onOpen={(i) => setOpenId(i.id)}
          no="01.5"
          title={t('overview.table')}
          emptyAction={
            <Button asChild size="sm" className="rounded-[2px]">
              <Link to="/instances/new">
                <Plus /> {t('instances.add')}
              </Link>
            </Button>
          }
          headerExtra={
            <Button asChild size="sm" variant="outline" className="rounded-[2px] mobile:hidden">
              <Link to="/instances/new">
                <Plus /> {t('instances.add')}
              </Link>
            </Button>
          }
        />
      </div>

      <InstanceDetailDrawer
        id={openId}
        instance={opened}
        synced={synced}
        now={now}
        outputs={openedPlugin?.outputs}
        pluginName={openedPlugin?.name}
        actions={manager.actions}
        onClose={closeDrawer}
      />
      {manager.dialogs}
    </>
  )
}

