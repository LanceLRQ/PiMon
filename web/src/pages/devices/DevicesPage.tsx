import { CircleArrowUp, HardDrive, Plus, Search } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { formatUptime } from '@/lib/format'
import { useNow } from '@/lib/time'
import { useIsMobile } from '@/lib/use-mobile'
import { useUptimeSeconds } from '@/lib/use-uptime'
import { cn } from '@/lib/utils'
import { useSystemInfo } from '@/pages/system/use-system'
import { selectDevices, selectInstances, useLiveStore, type LiveState } from '@/store/live-store'
import type { Proxy } from '@/types/generated'
import { Button } from '@/ui/button'
import { Dialog, DialogClose, DialogContent } from '@/ui/dialog'
import { NumberTag } from '@/ui/numbered-label'
import { PageHeader } from '@/ui/page-header'
import { StatusShape } from '@/ui/status-shape'
import { useToast } from '@/ui/toast'
import { AddDeviceDialog } from './add/AddDeviceDialog'
import { DeviceCards, DeviceTableView } from './DeviceTable'
import { deviceCounts, hubVersionOf, matchesFilter, matchesQuery, type DeviceFilter } from './model'
import { deviceRow, hubRow, type DeviceRow } from './rows'

const selectSynced = (s: LiveState) => s.synced
const selectBuild = (s: LiveState) => s.build
const filters: DeviceFilter[] = ['all', 'online', 'bad', 'dormant', 'pending']

// 设备页：汇总条 + 设备列表（中枢固定首行）。数据来自实时 store 的 devices 主题，
// 行点击与 ?open=<id> 深链共用同一个参数，详情抽屉在后续任务接入。
export function DevicesPage() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const mobile = useIsMobile()
  const now = useNow()
  const devices = useLiveStore(selectDevices)
  const instances = useLiveStore(selectInstances)
  const build = useLiveStore(selectBuild)
  const synced = useLiveStore(selectSynced)
  const { info } = useSystemInfo()
  const uptime = useUptimeSeconds(info?.uptime_seconds)
  const [params, setParams] = useSearchParams()
  const openId = params.get('open')
  const [filter, setFilter] = useState<DeviceFilter>('all')
  const [query, setQuery] = useState('')
  const [addOpen, setAddOpen] = useState(false)
  const [deleting, setDeleting] = useState<DeviceRow | null>(null)
  const [proxies, setProxies] = useState<Proxy[]>([])

  const hubVersion = info?.version ?? hubVersionOf(build)
  const needProxies = devices?.some((d) => d.direction === 'dial_out' && d.proxy_id) ?? false
  useEffect(() => {
    if (!needProxies) return
    let live = true
    http.get<Proxy[]>('/api/proxies').then(
      (list) => live && setProxies(list),
      () => {},
    )
    return () => {
      live = false
    }
  }, [needProxies])

  // 实例还没同步时数量未知，不当作 0
  const instanceCounts = useMemo(() => {
    if (!synced) return null
    const m = new Map<string, number>()
    for (const i of instances) m.set(i.runs_on, (m.get(i.runs_on) ?? 0) + 1)
    return m
  }, [instances, synced])

  const hub = useMemo(
    () =>
      hubRow(t, {
        version: info?.version ?? '—',
        platform: info ? `${info.os}/${info.arch}` : '—',
        uptimeText: uptime !== null ? formatUptime(t, uptime) : null,
        instances: instanceCounts ? (instanceCounts.get('hub') ?? 0) : null,
      }),
    [t, info, uptime, instanceCounts],
  )
  const allRows = useMemo(
    () => (devices ?? []).map((d) => deviceRow(t, d, { devices: devices ?? [], hubVersion, now, proxies, instanceCounts })),
    [t, devices, hubVersion, now, proxies, instanceCounts],
  )
  const visible = useMemo(
    () => allRows.filter((r) => r.device && matchesFilter(r.device, filter) && matchesQuery(r.device, query)),
    [allRows, filter, query],
  )
  const rows = [hub, ...visible]
  const counts = useMemo(() => (devices ? deviceCounts(devices, hubVersion) : null), [devices, hubVersion])
  const total = devices?.length ?? 0
  const filterActive = filter !== 'all' || query.trim() !== ''
  const dialIn = devices?.filter((d) => d.direction === 'dial_in').length ?? 0
  const dialOut = devices?.filter((d) => d.direction === 'dial_out').length ?? 0

  const openById = useCallback(
    (id: string) => setParams((p) => { const n = new URLSearchParams(p); n.set('open', id); return n }, { replace: true }),
    [setParams],
  )
  const onOpen = useCallback((row: DeviceRow) => openById(row.id), [openById])

  async function confirmDelete() {
    const row = deleting
    if (!row) return
    try {
      await http.delete(`/api/devices/${row.id}`)
      toast.show(t('devices.list.deleted', { name: row.name }))
      setDeleting(null)
    } catch (e) {
      toast.show(translateErrorValue(i18n, e), 'warn')
    }
  }

  const addButton = (
    <Button size="sm" className="rounded-[2px]" onClick={() => setAddOpen(true)}>
      <Plus /> {t('devices.list.add')}
    </Button>
  )

  const num = (n: number | undefined) => (counts && n !== undefined ? n : '—')
  const stripCell = (icon: ReactNode, label: string, value: string | number) => (
    <div className="border-t border-l border-border px-4 py-2.5">
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground">{icon}{label}</div>
      <div className="mt-0.5 font-mono text-xl tabular-nums">{value}</div>
    </div>
  )

  let below: ReactNode = null
  if (devices === null) {
    below = <div className="border-t border-border px-4 py-6 text-center text-[13px] text-muted-foreground">{t('devices.list.loading')}</div>
  } else if (devices.length === 0) {
    below = (
      <div className="flex flex-col items-center gap-2.5 border-t border-border px-4 py-10 text-center text-[13px] text-muted-foreground">
        <HardDrive size={24} aria-hidden="true" />
        <h3 className="text-[15px] font-medium text-foreground">{t('devices.list.empty.title')}</h3>
        <p className="m-0 max-w-[460px] leading-[1.6]">{t('devices.list.empty.body')}</p>
        {addButton}
      </div>
    )
  } else if (visible.length === 0) {
    below = (
      <div className="flex flex-col items-center gap-2.5 border-t border-border px-4 py-8 text-center text-[13px] text-muted-foreground">
        <span>{t('devices.list.noMatch')}</span>
        {filterActive && (
          <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => { setFilter('all'); setQuery('') }}>
            {t('devices.list.clearFilter')}
          </Button>
        )}
      </div>
    )
  }

  const legend: [string, string][] = [
    ['ok', 'online'], ['disconnected', 'disconnected'], ['offline', 'offline'], ['dormant', 'dormant'], ['pending', 'pending'],
  ]
  const handlers = { openId, onOpen, onDeletePlaceholder: setDeleting }

  return (
    <>
      <PageHeader
        no="07"
        title={t('pages.devices')}
        sub={devices ? t('devices.list.sub', { count: total, in: dialIn, out: dialOut }) : undefined}
        actions={addButton}
      />
      <div className="flex flex-col gap-4 p-6 mobile:p-3.5">
        <section aria-label={t('devices.list.stripLabel')} className="overflow-hidden rounded-[2px] border border-line-strong bg-card">
          <div className="-mt-px -ml-px grid grid-cols-6 mobile:grid-cols-3">
            {stripCell(<StatusShape state="ok" />, t('devices.list.strip.online'), num(counts?.online))}
            {stripCell(<StatusShape state="disconnected" />, t('devices.list.strip.disconnected'), num(counts?.disconnected))}
            {stripCell(<StatusShape state="offline" className="text-status-warn" />, t('devices.list.strip.offline'), num(counts?.offline))}
            {stripCell(<StatusShape state="dormant" />, t('devices.list.strip.dormant'), num(counts?.dormant))}
            {stripCell(<StatusShape state="pending" />, t('devices.list.strip.pending'), num(counts?.pending))}
            {stripCell(<CircleArrowUp size={14} className="text-status-warn" aria-hidden="true" />, t('devices.list.strip.upgrade'), num(counts?.upgrade))}
          </div>
        </section>

        <section aria-label={t('devices.list.listTitle')} className="rounded-[2px] border border-line-strong bg-card">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border px-4 py-2.5">
            <div className="flex items-center gap-2">
              <NumberTag no="07.1" />
              <h2 className="text-[14px] font-medium">{t('devices.list.listTitle')}</h2>
            </div>
            <div className="relative min-w-[200px] flex-1 sm:max-w-[320px]">
              <Search size={14} className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted-foreground" />
              <input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t('devices.list.search')}
                aria-label={t('devices.list.searchLabel')}
                className="h-8 w-full rounded-[2px] border border-line-strong bg-card pr-2.5 pl-8 text-[12.5px] outline-none placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring"
              />
            </div>
            <div role="group" aria-label={t('devices.list.filter.label')} className="inline-flex max-w-full overflow-x-auto rounded-[2px] border border-line-strong bg-card text-xs">
              {filters.map((f, i) => {
                const on = filter === f
                const n = f === 'all' ? total : f === 'bad' ? counts?.bad : counts?.[f]
                return (
                  <button
                    key={f}
                    type="button"
                    aria-pressed={on}
                    onClick={() => setFilter(f)}
                    className={cn('h-7 shrink-0 px-2.5 whitespace-nowrap', i > 0 && 'border-l border-border', on ? 'bg-inv-bg text-inv-ink' : 'hover:bg-panel-2')}
                  >
                    {t(`devices.list.filter.${f}`)} <span className="font-mono">{devices ? n : '—'}</span>
                  </button>
                )
              })}
            </div>
          </div>

          {mobile ? <DeviceCards rows={rows} {...handlers} /> : <DeviceTableView rows={rows} {...handlers} />}
          {below}

          <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1 border-t border-border bg-panel-2/50 px-4 py-2 text-[12px] text-muted-foreground mobile:hidden">
            {legend.map(([shape, key]) => (
              <span key={key} className="inline-flex items-center gap-1">
                <StatusShape state={shape} size={12} />
                {t(`devices.list.legend.${key}`)}
              </span>
            ))}
            <span className="inline-flex items-center gap-1">
              <CircleArrowUp size={12} aria-hidden="true" />
              {t('devices.list.legend.upgrade')}
            </span>
          </div>
          <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-border px-4 py-2 font-mono text-[11px] text-muted-foreground">
            <span>
              {t('devices.list.count', { shown: visible.length, total })}
              {filterActive ? t('devices.list.filtered') : ''}
            </span>
            <span className="mobile:hidden">{t('devices.list.pinnedNote')}</span>
          </div>
        </section>
      </div>

      <AddDeviceDialog open={addOpen} onOpenChange={setAddOpen} onViewDetail={openById} />

      <Dialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <DialogContent
          title={t('devices.list.deleteTitle')}
          tag="!"
          tagTone="crit"
          description={t('devices.list.deleteBody', { name: deleting?.name ?? '' })}
          footer={
            <>
              <DialogClose asChild><Button variant="outline" size="sm" className="rounded-[2px]">{t('common.cancel')}</Button></DialogClose>
              <Button variant="destructive" size="sm" className="rounded-[2px]" onClick={() => void confirmDelete()}>{t('devices.list.deleteConfirm')}</Button>
            </>
          }
        >
          {t('devices.list.deleteBody', { name: deleting?.name ?? '' })}
        </DialogContent>
      </Dialog>
    </>
  )
}
