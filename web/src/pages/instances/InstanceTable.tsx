import { ChevronDown, ChevronsUpDown, MoreHorizontal, Pause, Play, RefreshCw, Search, Settings2, SquareArrowOutUpRight, Copy, Trash2 } from 'lucide-react'
import { Fragment, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useIsMobile } from '@/lib/use-mobile'
import { formatAgo, parseTime } from '@/lib/time'
import { cn } from '@/lib/utils'
import type { Instance } from '@/types/generated'
import { Button } from '@/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/ui/dropdown-menu'
import { NumberTag } from '@/ui/numbered-label'
import { StatusLabel, StatusShape, isDimmedState } from '@/ui/status-shape'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/ui/table'
import { WordmarkBadge } from '@/ui/wordmark-badge'
import type { InstanceActions } from './actions'
import {
  defaultDir,
  emptyFilter,
  filterInstances,
  isAttention,
  sortInstances,
  type SortDir,
  type SortKey,
  type TableFilter,
} from './query'

interface InstanceTableProps {
  instances: Instance[]
  // 是否已收到过 snapshot；未收到时显示加载中，而不是空列表
  synced: boolean
  now: number
  actions: InstanceActions
  onOpen(inst: Instance): void
  // 表头编号与标题，如 01.5 / 实例一览
  no: string
  title: string
  // 表头右侧附加内容（添加实例按钮、视图切换等）
  headerExtra?: ReactNode
  // 勾选与按插件分组仅实例列表页使用
  selectable?: boolean
  selected?: ReadonlySet<string>
  onSelectedChange?(ids: Set<string>): void
  grouped?: boolean
  // 空表时的引导（没有任何实例）
  emptyAction?: ReactNode
  // 表头与表体之间（批量操作条）、表体与计数行之间（目录提示）的附加内容
  belowHead?: ReactNode
  aboveFoot?: ReactNode
}

const columns: { key: SortKey | null; label: string }[] = [
  { key: 'status', label: 'instances.cols.status' },
  { key: 'name', label: 'instances.cols.name' },
  { key: 'run', label: 'instances.cols.runs' },
  { key: null, label: 'instances.cols.reading' },
  { key: 'ago', label: 'instances.cols.updated' },
]

function readingOf(inst: Instance): string {
  return inst.summary || inst.issue || inst.last_error || ''
}

function runsLabel(t: (k: string) => string, runsOn: string) {
  return runsOn === 'hub' ? t('instances.hubLocal') : runsOn
}

// 实例表：总览与实例列表页共用。查询语法 + 筛选 + 排序 + 行菜单；
// 行数据来自实时 store，patch 到达时只有对应行变化；「更新于」随 now 每秒刷新。
export function InstanceTable({
  instances,
  synced,
  now,
  actions,
  onOpen,
  no,
  title,
  headerExtra,
  selectable,
  selected,
  onSelectedChange,
  grouped,
  emptyAction,
  belowHead,
  aboveFoot,
}: InstanceTableProps) {
  const { t, i18n } = useTranslation()
  const mobile = useIsMobile()
  const [filter, setFilter] = useState<TableFilter>(emptyFilter)
  const [sort, setSort] = useState<{ key: SortKey; dir: SortDir }>({ key: 'status', dir: 'desc' })

  const locations = useMemo(() => [...new Set(instances.map((i) => i.runs_on))].sort(), [instances])
  const plugins = useMemo(() => [...new Set(instances.map((i) => i.plugin_id))].sort(), [instances])
  const attentionCount = useMemo(() => instances.filter((i) => isAttention(i)).length, [instances])
  const visible = useMemo(
    () => sortInstances(filterInstances(instances, filter), sort.key, sort.dir, i18n.language === 'en' ? 'en' : 'zh'),
    [instances, filter, sort, i18n.language],
  )

  // 筛选后看不到的行不再保留在选择里，避免批量操作静默作用于隐藏的行
  useEffect(() => {
    if (!selected || selected.size === 0 || !onSelectedChange) return
    const shown = new Set(visible.map((i) => i.id))
    const kept = [...selected].filter((id) => shown.has(id))
    if (kept.length !== selected.size) onSelectedChange(new Set(kept))
  }, [visible, selected, onSelectedChange])

  const showSelect = !!selectable && !mobile
  const colSpan = columns.length + 1 + (showSelect ? 1 : 0)
  const allSelected = visible.length > 0 && visible.every((i) => selected?.has(i.id))

  function toggleSort(key: SortKey) {
    setSort((s) => (s.key === key ? { key, dir: s.dir === 'desc' ? 'asc' : 'desc' } : { key, dir: defaultDir(key) }))
  }

  function toggleOne(id: string) {
    const next = new Set(selected)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    onSelectedChange?.(next)
  }

  function toggleAll() {
    const next = new Set(selected)
    for (const i of visible) {
      if (allSelected) next.delete(i.id)
      else next.add(i.id)
    }
    onSelectedChange?.(next)
  }

  const filterActive = filter.attention || filter.location !== null || filter.plugin !== null || filter.query.trim() !== ''
  const sortLabel = t(`instances.sort.${sort.key}`) + (sort.key === 'status' && sort.dir === 'asc' ? t('instances.sort.reversed') : '')

  const groups = useMemo(() => {
    if (!grouped) return null
    const map = new Map<string, Instance[]>()
    for (const i of visible) map.set(i.plugin_id, [...(map.get(i.plugin_id) ?? []), i])
    return [...map.entries()].sort(([a], [b]) => a.localeCompare(b))
  }, [grouped, visible])

  function renderRow(inst: Instance) {
    const dimmed = isDimmedState(inst.display_state)
    const reading = readingOf(inst)
    const ago = formatAgo(t, now, parseTime(inst.last_success_at))
    const behind = inst.display_state === 'error' || inst.display_state === 'stale'
    return (
      <TableRow
        key={inst.id}
        dimmed={dimmed}
        data-state-row={inst.display_state}
        className={cn(inst.display_state === 'critical' && 'shadow-[inset_2px_0_0_var(--status-crit)]', selected?.has(inst.id) && 'bg-signal-soft')}
      >
        {showSelect && (
          <TableCell className="w-10 pr-0">
            <input
              type="checkbox"
              aria-label={t('instances.select', { name: inst.name })}
              checked={!!selected?.has(inst.id)}
              onChange={() => toggleOne(inst.id)}
              className="size-3.5 accent-[var(--inv-bg)]"
            />
          </TableCell>
        )}
        <TableCell className="whitespace-nowrap">
          <StatusLabel state={inst.display_state} />
        </TableCell>
        <TableCell className="h-auto py-1.5">
          <div className="flex items-center gap-2">
            <WordmarkBadge name={inst.plugin_id} size={22} />
            <div className="min-w-0">
              <button
                type="button"
                onClick={() => onOpen(inst)}
                className="block max-w-[220px] truncate text-left text-[13px] font-medium outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
              >
                {inst.name}
              </button>
              <div className="flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
                <span className="truncate">{inst.plugin_id}</span>
                {inst.paused && (
                  <span className="rounded-[2px] border border-line-strong px-1 text-[10px] text-ink-2">{t('instances.pausedTag')}</span>
                )}
              </div>
            </div>
          </div>
        </TableCell>
        <TableCell className="whitespace-nowrap">
          <span className="inline-flex items-center gap-1.5">
            <span className="rounded-[2px] border border-border px-1 font-mono text-[10.5px] text-muted-foreground">
              {inst.runs_on === 'hub' ? 'hub' : 'agent'}
            </span>
            <span className="text-[13px]">{inst.runs_on === 'hub' ? t('instances.local') : inst.runs_on}</span>
          </span>
        </TableCell>
        <TableCell mono className={cn('max-w-[260px] truncate text-[12.5px]', (inst.display_state === 'error' || !reading) && 'text-muted-foreground')} title={reading || t('items.unknown')}>
          {reading || '—'}
        </TableCell>
        <TableCell mono className="whitespace-nowrap text-[12.5px] text-muted-foreground">
          {behind ? (
            <span className="inline-flex items-center gap-1.5">
              <StatusShape state="stale" size={13} />
              {ago}
            </span>
          ) : (
            ago
          )}
        </TableCell>
        <TableCell className="w-[60px] text-right">
          <RowMenu inst={inst} actions={actions} onOpen={onOpen} />
        </TableCell>
      </TableRow>
    )
  }

  const head = (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border px-4 py-2.5">
      <div className="flex items-center gap-2">
        <NumberTag no={no} />
        <h2 className="text-[14px] font-medium">{title}</h2>
      </div>
      <div className="relative min-w-[200px] flex-1 sm:max-w-[360px]">
        <Search size={14} className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted-foreground" />
        <input
          value={filter.query}
          onChange={(e) => setFilter((f) => ({ ...f, query: e.target.value }))}
          placeholder="status:warning runs:hub plugin:demo"
          aria-label={t('instances.search')}
          className="h-8 w-full rounded-[2px] border border-line-strong bg-card pr-2.5 pl-8 font-mono text-[12.5px] outline-none placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring"
        />
      </div>
      <div role="group" aria-label={t('instances.filter.label')} className="inline-flex overflow-hidden rounded-[2px] border border-line-strong bg-card text-xs">
        <button
          type="button"
          aria-pressed={!filter.attention && !filter.location && !filter.plugin}
          onClick={() => setFilter((f) => ({ ...f, attention: false, location: null, plugin: null }))}
          className={cn('h-7 px-2.5', !filter.attention && !filter.location && !filter.plugin ? 'bg-inv-bg text-inv-ink' : 'hover:bg-panel-2')}
        >
          {t('instances.filter.all')} <span className="font-mono">{instances.length}</span>
        </button>
        <button
          type="button"
          aria-pressed={filter.attention}
          onClick={() => setFilter((f) => ({ ...f, attention: !f.attention }))}
          className={cn('h-7 border-l border-border px-2.5', filter.attention ? 'bg-inv-bg text-inv-ink' : 'hover:bg-panel-2')}
        >
          {t('instances.filter.attention')} <span className="font-mono">{attentionCount}</span>
        </button>
        <FilterMenu
          label={filter.location ? t('instances.filter.locationSet', { value: filter.location }) : t('instances.filter.location')}
          active={filter.location !== null}
          title={t('instances.filter.location')}
          allLabel={t('instances.filter.allLocations')}
          options={locations.map((l) => ({ value: l, label: runsLabel(t, l) }))}
          value={filter.location}
          onChange={(location) => setFilter((f) => ({ ...f, location }))}
        />
        <FilterMenu
          label={filter.plugin ? t('instances.filter.pluginSet', { value: filter.plugin }) : t('instances.filter.plugin')}
          active={filter.plugin !== null}
          title={t('instances.filter.plugin')}
          allLabel={t('instances.filter.allPlugins')}
          options={plugins.map((p) => ({ value: p, label: p }))}
          value={filter.plugin}
          onChange={(plugin) => setFilter((f) => ({ ...f, plugin }))}
        />
      </div>
      <div className="ml-auto flex items-center gap-2">{headerExtra}</div>
    </div>
  )

  let body: ReactNode
  if (!synced) {
    body = <div className="px-4 py-8 text-center text-[13px] text-muted-foreground">{t('shell.loading')}</div>
  } else if (instances.length === 0) {
    body = (
      <div className="flex flex-col items-center gap-2.5 px-4 py-10 text-center text-[13px] text-muted-foreground">
        <span>{t('instances.empty')}</span>
        {emptyAction}
      </div>
    )
  } else if (visible.length === 0) {
    body = (
      <div className="flex flex-col items-center gap-2.5 px-4 py-8 text-center text-[13px] text-muted-foreground">
        <span>{t('instances.noMatch')}</span>
        {filterActive && (
          <Button variant="outline" size="sm" className="rounded-[2px]" onClick={() => setFilter(emptyFilter)}>
            {t('instances.clearFilter')}
          </Button>
        )}
      </div>
    )
  } else if (mobile) {
    body = (
      <ul className="m-0 list-none p-0">
        {visible.map((inst) => (
          <li key={inst.id} className={cn('border-t border-border first:border-t-0', isDimmedState(inst.display_state) && 'opacity-60')}>
            <button
              type="button"
              onClick={() => onOpen(inst)}
              className="grid w-full grid-cols-[auto_1fr_auto] items-center gap-3 px-4 py-2.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <StatusShape state={inst.display_state} size={16} />
              <span className="min-w-0">
                <span className="block truncate text-[14px] font-medium">{inst.name}</span>
                <span className="block truncate font-mono text-[11px] text-muted-foreground">
                  {inst.plugin_id} · {runsLabel(t, inst.runs_on)}
                </span>
              </span>
              <span className="max-w-[40vw] text-right">
                <span className="block truncate font-mono text-[12.5px]">{readingOf(inst) || '—'}</span>
                <span className="block font-mono text-[11px] text-muted-foreground">{formatAgo(t, now, parseTime(inst.last_success_at))}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
    )
  } else {
    body = (
      <Table className="border-0">
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            {showSelect && (
              <TableHead className="w-10">
                <input
                  type="checkbox"
                  aria-label={t('instances.selectAll')}
                  checked={allSelected}
                  onChange={toggleAll}
                  className="size-3.5 accent-[var(--inv-bg)]"
                />
              </TableHead>
            )}
            {columns.map((c) => {
              if (!c.key) return <TableHead key={c.label}>{t(c.label)}</TableHead>
              const on = sort.key === c.key
              const key = c.key
              return (
                <TableHead key={c.label} aria-sort={on ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}>
                  <button
                    type="button"
                    onClick={() => toggleSort(key)}
                    className={cn('inline-flex items-center gap-1 outline-none focus-visible:ring-2 focus-visible:ring-ring', on && 'text-foreground')}
                  >
                    {t(c.label)}
                    <span aria-hidden="true">{on ? (sort.dir === 'asc' ? '↑' : '↓') : <ChevronsUpDown size={11} />}</span>
                    {on && key === 'status' && sort.dir === 'desc' && <span className="text-signal-text">{t('instances.severityFirst')}</span>}
                  </button>
                </TableHead>
              )
            })}
            <TableHead className="w-[60px]">
              <span className="sr-only">{t('instances.cols.actions')}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {groups
            ? groups.map(([plugin, list]) => {
                const bad = list.filter((i) => i.display_state !== 'ok')
                return (
                  <Fragment key={plugin}>
                    <TableRow className="bg-panel-2/70 hover:bg-panel-2/70">
                      <TableCell colSpan={colSpan} className="h-7">
                        <span className="inline-flex flex-wrap items-center gap-2 font-mono text-[12px]">
                          {plugin}
                          <span className="text-muted-foreground">{t('instances.groupCount', { count: list.length })}</span>
                          {bad.length === 0 ? (
                            <span className="inline-flex items-center gap-1 font-sans text-muted-foreground">
                              <StatusShape state="ok" size={12} />
                              {t('instances.groupAllOk')}
                            </span>
                          ) : (
                            bad.map((i) => <StatusShape key={i.id} state={i.display_state} size={12} />)
                          )}
                        </span>
                      </TableCell>
                    </TableRow>
                    {list.map(renderRow)}
                  </Fragment>
                )
              })
            : visible.map(renderRow)}
        </TableBody>
      </Table>
    )
  }

  return (
    <section className="rounded-[2px] border border-line-strong bg-card" aria-label={title}>
      {head}
      {belowHead}
      {body}
      {aboveFoot}
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-border px-4 py-2 font-mono text-[11px] text-muted-foreground">
        <span>{t('instances.count', { shown: visible.length, total: instances.length, sort: sortLabel })}{grouped ? t('instances.groupedSuffix') : ''}</span>
        <span className="mobile:hidden">{t('instances.syntax')}</span>
      </div>
    </section>
  )
}

interface FilterMenuProps {
  label: string
  title: string
  allLabel: string
  active: boolean
  options: { value: string; label: string }[]
  value: string | null
  onChange(v: string | null): void
}

const ALL = '__all__'

function FilterMenu({ label, title, allLabel, active, options, value, onChange }: FilterMenuProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className={cn(
          'inline-flex h-7 items-center gap-1 border-l border-border px-2.5 outline-none focus-visible:ring-2 focus-visible:ring-ring',
          active ? 'bg-signal-soft font-medium' : 'hover:bg-panel-2',
        )}
      >
        <span>{label}</span>
        <ChevronDown size={12} />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-44 rounded-[2px]">
        <DropdownMenuLabel className="font-mono text-[10.5px] font-normal text-muted-foreground">{title}</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={value ?? ALL} onValueChange={(v) => onChange(v === ALL ? null : v)}>
          <DropdownMenuRadioItem value={ALL}>{allLabel}</DropdownMenuRadioItem>
          {options.map((o) => (
            <DropdownMenuRadioItem key={o.value} value={o.value}>
              {o.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

interface RowMenuProps {
  inst: Instance
  actions: InstanceActions
  onOpen(inst: Instance): void
}

export function RowMenu({ inst, actions, onOpen }: RowMenuProps) {
  const { t } = useTranslation()
  const running = actions.running.has(inst.id)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" className="size-7 rounded-[2px]" aria-label={t('instances.rowMenu', { name: inst.name })}>
          <MoreHorizontal size={15} />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48 rounded-[2px]">
        <DropdownMenuLabel className="max-w-56 truncate font-mono text-[10.5px] font-normal text-muted-foreground">{inst.name}</DropdownMenuLabel>
        <DropdownMenuItem onSelect={() => onOpen(inst)}>
          <SquareArrowOutUpRight /> {t('instances.menu.detail')}
        </DropdownMenuItem>
        <DropdownMenuItem disabled={running} onSelect={() => void actions.run(inst)}>
          <RefreshCw /> {t('instances.menu.run')}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.edit(inst)}>
          <Settings2 /> {t('instances.menu.edit')}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => void actions.copy(inst)}>
          <Copy /> {t('instances.menu.copy')}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void actions.setPaused(inst, !inst.paused)}>
          {inst.paused ? <Play /> : <Pause />} {inst.paused ? t('instances.menu.resume') : t('instances.menu.pause')}
        </DropdownMenuItem>
        <DropdownMenuItem variant="destructive" onSelect={() => actions.requestDelete([inst])}>
          <Trash2 /> {t('instances.menu.delete')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
