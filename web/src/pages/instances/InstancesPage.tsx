import { FolderOpen, Layers, List, Pause, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { translateErrorValue } from '@/i18n/errors'
import { useIsMobile } from '@/lib/use-mobile'
import { formatDateTime, useNow } from '@/lib/time'
import { selectInstances, useLiveStore, type LiveState } from '@/store/live-store'
import { Button } from '@/ui/button'
import { PageHeader } from '@/ui/page-header'
import { Segmented } from '@/ui/segmented'
import { StatusShape } from '@/ui/status-shape'
import { useToast } from '@/ui/toast'
import { useInstanceManager } from './actions'
import { InstanceDetailDrawer } from './InstanceDetailDrawer'
import { InstanceTable } from './InstanceTable'
import { countStates } from './query'
import { usePlugins } from './use-plugins'

const selectSynced = (s: LiveState) => s.synced

// 监控实例页：状态条 + 实例表（平铺 / 按插件分组、勾选与批量操作）+ exec 插件目录提示
export function InstancesPage() {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const mobile = useIsMobile()
  const instances = useLiveStore(selectInstances)
  const synced = useLiveStore(selectSynced)
  const now = useNow()
  const plugins = usePlugins()
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [grouped, setGrouped] = useState(false)
  const [openId, setOpenId] = useState<string | null>(null)
  const [rescanning, setRescanning] = useState(false)

  const manager = useInstanceManager(plugins.list?.plugins, (ids) =>
    setSelected((s) => new Set([...s].filter((id) => !ids.includes(id)))),
  )
  const { actions } = manager

  const counts = useMemo(() => countStates(instances), [instances])
  const pluginIds = useMemo(() => new Set(instances.map((i) => i.plugin_id)), [instances])
  const locations = useMemo(() => new Set(instances.map((i) => i.runs_on)), [instances])
  const execCount = plugins.list?.plugins.filter((p) => p.origin === 'exec').length ?? 0
  const hubCount = instances.filter((i) => i.runs_on === 'hub').length
  // 已被删除的实例不再算入选择
  const selectedList = useMemo(() => instances.filter((i) => selected.has(i.id)), [instances, selected])
  const closeDrawer = useCallback(() => setOpenId(null), [])
  const opened = openId ? instances.find((i) => i.id === openId) : undefined
  const openedPlugin = opened ? plugins.list?.plugins.find((p) => p.id === opened.plugin_id) : undefined

  async function rescan() {
    setRescanning(true)
    try {
      const before = new Set(plugins.list?.plugins.map((p) => p.id))
      const next = await plugins.rescan()
      const added = next.plugins.filter((p) => !before.has(p.id)).length
      toast.show(
        t('instances.rescanDone', {
          found: added > 0 ? t('instances.rescanFound', { count: added }) : t('instances.rescanNone'),
          time: formatDateTime(Date.now(), i18n.language),
        }),
      )
    } catch (e) {
      toast.show(translateErrorValue(i18n, e), 'warn')
    } finally {
      setRescanning(false)
    }
  }

  const batch = selectedList.length > 0 && (
    <div role="toolbar" aria-label={t('instances.batch.label')} className="flex flex-wrap items-center gap-2 border-b border-border bg-signal-soft px-4 py-2 text-[13px]">
      <span>{t('instances.batch.selected', { count: selectedList.length })}</span>
      <span className="flex-1" />
      <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => void actions.pauseMany(selectedList)}>
        <Pause /> {t('instances.menu.pause')}
      </Button>
      <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => void actions.runMany(selectedList)}>
        <RefreshCw /> {t('instances.menu.run')}
      </Button>
      <Button variant="outline" size="sm" className="rounded-[2px] text-destructive" onClick={() => actions.requestDelete(selectedList)}>
        <Trash2 /> {t('instances.menu.delete')}
      </Button>
      <Button variant="ghost" size="sm" className="rounded-[2px]" onClick={() => setSelected(new Set())}>
        {t('instances.batch.clear')}
      </Button>
    </div>
  )

  const dirNote = (
    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 border-t border-border bg-panel-2/50 px-4 py-2 text-[12.5px] text-ink-2">
      <FolderOpen size={15} className="shrink-0 text-muted-foreground" />
      <span className="min-w-0 break-all">
        {t('instances.execDir')}{' '}
        {plugins.list?.plugin_dir ? (
          <code className="font-mono text-[12px]">{plugins.list.plugin_dir}</code>
        ) : (
          <span className="text-muted-foreground">{plugins.error ? t('items.unknown') : t('shell.loading')}</span>
        )}{' '}
        {t('instances.execWatched')}
      </span>
      {plugins.list && plugins.list.errors.length > 0 && (
        <span className="inline-flex items-center gap-1 text-status-crit">
          <StatusShape state="error" size={12} />
          {t('instances.execErrors', { count: plugins.list.errors.length })}
        </span>
      )}
      <span className="flex-1" />
      <Button variant="outline" size="sm" className="rounded-[2px]" disabled={rescanning} onClick={() => void rescan()}>
        <RefreshCw className={rescanning ? 'animate-spin' : undefined} /> {t('instances.rescan')}
      </Button>
    </div>
  )

  const addButton = (
    <Button asChild size="sm" className="rounded-[2px]">
      <Link to="/instances/new">
        <Plus /> {t('instances.add')}
      </Link>
    </Button>
  )

  const stripCell = (label: React.ReactNode, value: React.ReactNode, sub?: string) => (
    <div className="border-t border-l border-border px-4 py-2.5">
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground">{label}</div>
      <div className="mt-0.5 font-mono text-xl tabular-nums">
        {value}
        {sub && <small className="text-xs text-muted-foreground">{sub}</small>}
      </div>
    </div>
  )

  return (
    <>
      <PageHeader
        no="03"
        title={t('pages.instances')}
        sub={synced ? t('instances.sub', { total: instances.length, hub: hubCount, other: instances.length - hubCount }) : undefined}
        actions={<span className="mobile:hidden">{addButton}</span>}
      />
      <div className="flex flex-col gap-4 p-6 mobile:p-3.5">
        <section aria-label={t('instances.stripLabel')} className="overflow-hidden rounded-[2px] border border-line-strong bg-card">
          <div className="-mt-px -ml-px grid grid-cols-6 mobile:grid-cols-3">
          {stripCell(<><StatusShape state="ok" />{t('status.ok')}</>, synced ? counts.ok : '—')}
          {stripCell(<><StatusShape state="warning" />{t('status.warning')}</>, synced ? counts.warning : '—')}
          {stripCell(<><StatusShape state="critical" />{t('status.critical')}</>, synced ? counts.critical : '—')}
          {stripCell(<><StatusShape state="error" />{t('status.error')}</>, synced ? counts.error : '—')}
          {stripCell(
            t('instances.strip.plugins'),
            synced ? pluginIds.size : '—',
            plugins.list ? t('instances.strip.exec', { count: execCount }) : undefined,
          )}
          {stripCell(t('instances.strip.locations'), synced ? locations.size : '—')}
          </div>
        </section>

        <InstanceTable
          instances={instances}
          synced={synced}
          now={now}
          actions={actions}
          onOpen={(i) => setOpenId(i.id)}
          no="03.1"
          title={t('instances.listTitle')}
          selectable
          selected={selected}
          onSelectedChange={setSelected}
          grouped={grouped && !mobile}
          emptyAction={addButton}
          headerExtra={
            !mobile && (
              <Segmented
                ariaLabel={t('instances.view.label')}
                value={grouped ? 'group' : 'flat'}
                onChange={(v) => setGrouped(v === 'group')}
                options={[
                  { value: 'flat', label: <><List size={13} />{t('instances.view.flat')}</>, title: t('instances.view.flat') },
                  { value: 'group', label: <><Layers size={13} />{t('instances.view.group')}</>, title: t('instances.view.group') },
                ]}
              />
            )
          }
          belowHead={batch}
          aboveFoot={dirNote}
        />
      </div>

      <InstanceDetailDrawer
        id={openId}
        instance={opened}
        synced={synced}
        now={now}
        outputs={openedPlugin?.outputs}
        pluginName={openedPlugin?.name}
        actions={actions}
        onClose={closeDrawer}
      />
      {manager.dialogs}
    </>
  )
}
