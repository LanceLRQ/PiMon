import { CircleArrowUp, KeyRound, MoreHorizontal, SquareArrowOutUpRight, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { incompatibleText } from './model'
import { cn } from '@/lib/utils'
import { Button } from '@/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/ui/dropdown-menu'
import { StatusShape } from '@/ui/status-shape'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/ui/table'
import type { DeviceRow } from './rows'

interface Handlers {
  openId: string | null
  onOpen(row: DeviceRow): void
  onDeletePlaceholder(row: DeviceRow): void
  onRepair(row: DeviceRow): void
  onRevoke(row: DeviceRow): void
}

const toneText: Record<string, string> = {
  ok: 'text-status-ok',
  warn: 'text-status-warn',
  weak: 'text-muted-foreground',
  muted: 'text-muted-foreground',
  neutral: 'text-muted-foreground',
}

function useRoleLabel() {
  const { t, i18n } = useTranslation()
  return (role: string) => (i18n.exists(`devices.list.role.${role}`) ? t(`devices.list.role.${role}`) : role)
}

function Tag({ children, dim, title }: { children: React.ReactNode; dim?: boolean; title?: string }) {
  return (
    <span title={title} className={cn('inline-flex items-center rounded-[2px] border border-line-strong px-1 font-mono text-[10.5px]', dim ? 'text-muted-foreground' : 'text-ink-2')}>
      {children}
    </span>
  )
}

function StatusCell({ row }: { row: DeviceRow }) {
  const { t, i18n } = useTranslation()
  const { presence } = row
  return (
    <div className="min-w-0">
      <span className={cn('inline-flex items-center gap-1.5 text-[13px]', toneText[presence.tone])}>
        <StatusShape state={presence.shape} className={presence.tone === 'warn' ? 'text-status-warn' : undefined} />
        {t(presence.labelKey)}
      </span>
      {row.sub && <span className="block font-mono text-[11px] text-muted-foreground">{row.sub}</span>}
      {row.needsRepair && <span className="block text-[11.5px] text-status-warn">{t('devices.needsRepair')}</span>}
      {!row.needsRepair && row.connError && i18n.exists(`errors.pairing.reason.${row.connError}`) && (
        <span className="block text-[11.5px] text-muted-foreground">{t(`errors.pairing.reason.${row.connError}`)}</span>
      )}
    </div>
  )
}

function UpgradeTag({ row }: { row: DeviceRow }) {
  const { t, i18n } = useTranslation()
  if (!row.upgrade) return null
  if (row.upgrade.level === 'differs') {
    return <Tag dim title={t('devices.list.versionDiffers')}>{t('devices.upgrade.differs')}</Tag>
  }
  return (
    <span title={incompatibleText(i18n, row.upgradeReason)} className="inline-flex items-center gap-1 rounded-[2px] border border-status-warn px-1 text-[11px] font-medium text-status-warn">
      <CircleArrowUp size={12} aria-hidden="true" />
      {t(`devices.upgrade.${row.upgrade.target}`)}
    </span>
  )
}

function DirectionCell({ row }: { row: DeviceRow }) {
  const { t } = useTranslation()
  const key = row.direction === 'dial_out' ? 'dial_out' : row.direction === 'dial_in' ? 'dial_in' : 'local'
  return (
    <div>
      <span className="rounded-[2px] border border-border px-1 font-mono text-[10.5px] text-muted-foreground">{t(`devices.list.dir.${key}`)}</span>
      {row.direction === 'dial_out' && (
        <span className="block font-mono text-[11px] text-muted-foreground">
          {row.proxyName ? t('devices.list.dir.via', { proxy: row.proxyName }) : t('devices.list.dir.direct')}
        </span>
      )}
    </div>
  )
}

function FlagsCell({ row }: { row: DeviceRow }) {
  const { t } = useTranslation()
  if (row.hub || row.placeholder) return <span className="text-muted-foreground">—</span>
  return (
    <span className="flex flex-col items-start gap-0.5">
      {row.allowOffline ? <Tag>{t('devices.list.flag.allowOffline')}</Tag> : <Tag dim>{t('devices.list.flag.noOffline')}</Tag>}
      {row.maintenance && <Tag>{t('devices.list.flag.maintenance')}</Tag>}
    </span>
  )
}

function DeleteButton({ row, onDelete }: { row: DeviceRow; onDelete(row: DeviceRow): void }) {
  const { t } = useTranslation()
  if (row.placeholder !== 'dangling') return null
  return (
    <Button variant="outline" size="sm" className="rounded-[2px] text-destructive" onClick={() => onDelete(row)}>
      <Trash2 /> {t('devices.list.deletePlaceholder')}
    </Button>
  )
}

// 行菜单：查看详情、重新配对、吊销；中枢本机与悬空占位没有菜单（悬空占位用「删除占位」）
function RowMenu({ row, onOpen, onRepair, onRevoke }: Pick<Handlers, 'onOpen' | 'onRepair' | 'onRevoke'> & { row: DeviceRow }) {
  const { t } = useTranslation()
  if (row.hub || row.placeholder === 'dangling') return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" className="size-7 rounded-[2px]" aria-label={t('devices.list.menu.label')}>
          <MoreHorizontal size={15} />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44 rounded-[2px]">
        <DropdownMenuLabel className="max-w-56 truncate font-mono text-[10.5px] font-normal text-muted-foreground">{row.name}</DropdownMenuLabel>
        <DropdownMenuItem onSelect={() => onOpen(row)}>
          <SquareArrowOutUpRight /> {t('devices.list.menu.detail')}
        </DropdownMenuItem>
        <DropdownMenuItem disabled={row.placeholder !== null} onSelect={() => onRepair(row)}>
          <KeyRound /> {t('devices.list.menu.repair')}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onSelect={() => onRevoke(row)}>
          <Trash2 /> {t('devices.list.menu.revoke')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function DeviceTableView({ rows, openId, onOpen, onDeletePlaceholder, onRepair, onRevoke }: Handlers & { rows: DeviceRow[] }) {
  const { t } = useTranslation()
  const roleLabel = useRoleLabel()
  const cols = ['status', 'name', 'direction', 'address', 'platform', 'version', 'instances', 'flags']
  return (
    <Table className="border-0">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          {cols.map((c) => (
            <TableHead key={c}>{t(`devices.list.cols.${c}`)}</TableHead>
          ))}
          <TableHead className="w-[1%]">
            <span className="sr-only">{t('devices.list.cols.actions')}</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <TableRow
            key={row.id}
            data-device-id={row.id}
            aria-current={openId === row.id ? 'true' : undefined}
            dimmed={row.presence.shape === 'dormant'}
            className={cn('align-top', row.hub && 'bg-panel-2/40', openId === row.id && 'bg-signal-soft')}
          >
            <TableCell className="h-auto py-2"><StatusCell row={row} /></TableCell>
            <TableCell className="h-auto py-2">
              <button
                type="button"
                aria-label={t('devices.list.openRow', { name: row.name })}
                onClick={() => onOpen(row)}
                className="block max-w-[220px] text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <span className="block truncate text-[13px] font-medium hover:underline">{row.name}</span>
                <span className="block truncate font-mono text-[11px] text-muted-foreground">
                  {roleLabel(row.role)}
                  {!row.hub && row.hostname ? ` · ${row.hostname}` : ''}
                </span>
              </button>
            </TableCell>
            <TableCell className="h-auto py-2"><DirectionCell row={row} /></TableCell>
            <TableCell mono className="h-auto max-w-[200px] truncate py-2 text-[12.5px]" title={row.address}>{row.address}</TableCell>
            <TableCell mono className="h-auto whitespace-nowrap py-2 text-[12.5px]">{row.platform}</TableCell>
            <TableCell mono className="h-auto py-2 text-[12.5px]">
              {row.version}
              {row.upgrade && <div className="mt-0.5 font-sans"><UpgradeTag row={row} /></div>}
            </TableCell>
            <TableCell mono className="h-auto py-2 text-[12.5px]">{row.instances ?? '—'}</TableCell>
            <TableCell className="h-auto py-2"><FlagsCell row={row} /></TableCell>
            <TableCell className="h-auto py-2 text-right">
              <span className="inline-flex items-center gap-1">
                <DeleteButton row={row} onDelete={onDeletePlaceholder} />
                <RowMenu row={row} onOpen={onOpen} onRepair={onRepair} onRevoke={onRevoke} />
              </span>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export function DeviceCards({ rows, openId, onOpen, onDeletePlaceholder, onRepair, onRevoke }: Handlers & { rows: DeviceRow[] }) {
  const { t } = useTranslation()
  const roleLabel = useRoleLabel()
  return (
    <ul className="m-0 list-none p-0">
      {rows.map((row) => (
        <li
          key={row.id}
          aria-current={openId === row.id ? 'true' : undefined}
          className={cn('border-t border-border first:border-t-0', row.presence.shape === 'dormant' && 'opacity-60', row.hub && 'bg-panel-2/40', openId === row.id && 'bg-signal-soft')}
        >
          <button
            type="button"
            aria-label={t('devices.list.openRow', { name: row.name })}
            onClick={() => onOpen(row)}
            className="grid w-full grid-cols-[auto_1fr_auto] items-start gap-3 px-4 py-2.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <span className="pt-0.5"><StatusShape state={row.presence.shape} size={16} className={row.presence.tone === 'warn' ? 'text-status-warn' : undefined} /></span>
            <span className="min-w-0">
              <span className="block truncate text-[14px] font-medium">{row.name}</span>
              <span className="block truncate font-mono text-[11px] text-muted-foreground">
                {roleLabel(row.role)}
                {!row.hub ? ` · ${row.address}` : ''}
              </span>
              <span className="mt-1 flex flex-wrap gap-1">
                <Tag dim>
                  {t(`devices.list.dir.${row.direction === 'dial_out' ? 'dial_out' : row.direction === 'dial_in' ? 'dial_in' : 'local'}`)}
                  {row.direction === 'dial_out' ? ` · ${row.proxyName ? t('devices.list.dir.via', { proxy: row.proxyName }) : t('devices.list.dir.direct')}` : ''}
                </Tag>
                {row.platform !== '—' && <Tag dim>{row.platform}</Tag>}
                {row.version !== '—' && <Tag dim>{row.version}</Tag>}
                {!row.hub && !row.placeholder && row.allowOffline && <Tag>{t('devices.list.flag.allowOffline')}</Tag>}
                {row.maintenance && <Tag>{t('devices.list.flag.maintenance')}</Tag>}
                <UpgradeTag row={row} />
              </span>
            </span>
            <span className="max-w-[40vw] text-right">
              <span className={cn('block text-[12.5px]', toneText[row.presence.tone])}>{t(row.presence.labelKey)}</span>
              {row.sub && <span className="block font-mono text-[11px] text-muted-foreground">{row.sub}</span>}
            </span>
          </button>
          {!row.hub && (
            <div className="flex justify-end gap-1 px-4 pb-2.5">
              <DeleteButton row={row} onDelete={onDeletePlaceholder} />
              <RowMenu row={row} onOpen={onOpen} onRepair={onRepair} onRevoke={onRevoke} />
            </div>
          )}
        </li>
      ))}
    </ul>
  )
}
