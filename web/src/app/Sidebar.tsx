import { LogOut } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useLocation, useNavigate } from 'react-router'
import { logout } from '@/api/session'
import { translateErrorValue } from '@/i18n/errors'
import { selectConnected, useLiveStore } from '@/store/live-store'
import { cn } from '@/lib/utils'
import { PiMonLogo } from '@/ui/pimon-logo'
import { StatusShape } from '@/ui/status-shape'
import { isItemActive, isSubActive, laterItems, navItems, type NavItem } from './nav'
import { LanguageSwitch, ThemeSwitch } from './preferences'
import { useSession } from './session'

const itemBase = 'grid h-[34px] grid-cols-[24px_18px_1fr_auto] items-center gap-2 rounded-[2px] px-2 text-left text-sm'

function ActiveItem({ item, active, hasSub }: { item: NavItem; active: boolean; hasSub: boolean }) {
  const { t } = useTranslation()
  const Icon = item.icon
  return (
    <Link
      to={item.sub ? item.sub[0].to : item.to}
      aria-current={active && !hasSub ? 'page' : undefined}
      className={cn(itemBase, active ? 'bg-inv-bg text-inv-ink' : 'text-ink-2 hover:bg-panel-2 hover:text-foreground')}
    >
      <span className={cn('font-mono text-[11px]', active ? 'text-signal' : 'text-muted-foreground')}>{item.no}</span>
      <Icon size={16} aria-hidden="true" />
      <span>{t(item.labelKey)}</span>
      <span />
    </Link>
  )
}

function DisabledItem({ item }: { item: NavItem }) {
  const { t } = useTranslation()
  const Icon = item.icon
  return (
    <button
      type="button"
      disabled
      title={t('nav.laterHint', { milestone: item.milestone?.toUpperCase() })}
      className={cn(itemBase, 'w-full cursor-not-allowed text-muted-foreground')}
    >
      <span className="font-mono text-[11px]">{item.no}</span>
      <Icon size={16} aria-hidden="true" />
      <span>{t(item.labelKey)}</span>
      <span className="rounded-[2px] border border-line-strong px-1 font-mono text-[10px]">{item.milestone}</span>
    </button>
  )
}

// 桌面侧栏：编号导航、屏幕/实例二级菜单、后续里程碑灰显入口，底部是用户、语言与主题
export function Sidebar() {
  const { t, i18n } = useTranslation()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { markSignedOut } = useSession()
  const connected = useLiveStore(selectConnected)
  const [logoutError, setLogoutError] = useState<string | null>(null)

  async function onLogout() {
    setLogoutError(null)
    try {
      await logout()
      markSignedOut()
      navigate('/login', { replace: true })
    } catch (e) {
      setLogoutError(`${t('shell.logoutFailed')}：${translateErrorValue(i18n, e)}`)
    }
  }

  return (
    <aside aria-label={t('nav.primary')} className="flex w-56 flex-none flex-col border-r border-border bg-card mobile:hidden">
      <div className="flex h-14 items-center gap-2.5 border-b border-border px-4">
        <PiMonLogo className="size-[30px] flex-none" />
        <b className="text-lg font-medium tracking-tight">{t('app.name')}</b>
        <span
          className="ml-auto"
          title={connected ? t('shell.connection.online') : t('shell.connection.offline')}
        >
          <StatusShape state={connected ? 'ok' : 'offline'} size={12} />
        </span>
      </div>

      <nav className="flex-1 overflow-y-auto p-2">
        {navItems.map((item) => {
          const active = isItemActive(item, pathname)
          return (
            <div key={item.no}>
              <ActiveItem item={item} active={active} hasSub={!!item.sub} />
              {item.sub && (
                <div className="my-0.5 mb-1.5 ml-8 flex flex-col border-l border-border">
                  {item.sub.map((s) => {
                    const subActive = active && isSubActive(s, pathname)
                    return (
                      <Link
                        key={s.key}
                        to={s.to}
                        aria-current={subActive ? 'page' : undefined}
                        className={cn(
                          'flex h-7 items-center gap-2 px-2.5 text-[13px]',
                          subActive ? 'text-foreground shadow-[inset_2px_0_0_var(--signal)]' : 'text-ink-2 hover:text-foreground',
                        )}
                      >
                        <span className="w-2.5 font-mono text-[11px] text-muted-foreground">{s.key}</span>
                        {t(s.labelKey)}
                      </Link>
                    )
                  })}
                </div>
              )}
            </div>
          )
        })}
        <div className="px-2 pb-1.5 pt-2.5 font-mono text-[11px] lowercase text-muted-foreground">{t('nav.later')}</div>
        {laterItems.map((item) => (
          <DisabledItem key={item.no} item={item} />
        ))}
      </nav>

      <div className="border-t border-border p-3">
        <div className="mb-3 flex items-center gap-2.5">
          <div className="grid size-[30px] place-items-center rounded-[2px] border border-foreground font-mono text-[13px]">A</div>
          <div className="text-[13px] leading-tight">
            {t('shell.admin')}
            <small className="block font-mono text-[11px] text-muted-foreground">admin</small>
          </div>
          <button
            type="button"
            onClick={() => void onLogout()}
            title={t('shell.logout')}
            aria-label={t('shell.logout')}
            className="ml-auto p-1.5 text-muted-foreground hover:text-foreground"
          >
            <LogOut size={16} />
          </button>
        </div>
        {logoutError && (
          <p role="alert" className="mb-2 text-xs text-destructive">
            {logoutError}
          </p>
        )}
        <div className="flex gap-2">
          <LanguageSwitch className="flex-none" />
          <ThemeSwitch className="flex-1" />
        </div>
      </div>
    </aside>
  )
}
