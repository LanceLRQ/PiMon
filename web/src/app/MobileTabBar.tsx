import { LogOut, Moon, Sun } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Link, useLocation, useNavigate } from 'react-router'
import { logout } from '@/api/session'
import { setThemeChoice, useThemeChoice } from '@/admin-theme/theme'
import { setLanguage } from '@/i18n'
import { cn } from '@/lib/utils'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/ui/dropdown-menu'
import { activeMobileTab, mobileTabs } from './nav'
import { useSession } from './session'

const tabClass = 'flex flex-col items-center justify-center gap-[3px] text-[11px]'

// 「更多」菜单里的页面：屏幕分组的其余入口、代理、系统
const moreLinks = [
  { labelKey: 'nav.sub.screenManage', to: '/screens' },
  { labelKey: 'nav.sub.layoutEditor', to: '/screens/editor' },
  { labelKey: 'nav.sub.schedule', to: '/screens/schedule' },
  { labelKey: 'nav.sub.remote', to: '/screens/remote' },
  { labelKey: 'nav.proxies', to: '/proxies' },
  { labelKey: 'nav.system', to: '/system' },
]

// 手机底部标签栏（≤640px 显示）：总览、屏幕、实例、设置、更多
export function MobileTabBar() {
  const { t, i18n } = useTranslation()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { markSignedOut } = useSession()
  const theme = useThemeChoice()
  const current = activeMobileTab(pathname)
  const darkNow = theme === 'dark' || (theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)

  async function onLogout() {
    try {
      await logout()
      markSignedOut()
      navigate('/login', { replace: true })
    } catch {
      // 失败时留在当前页，用户可重试
    }
  }

  return (
    <nav
      aria-label={t('nav.mobile')}
      className="fixed inset-x-0 bottom-0 z-30 hidden h-16 grid-cols-5 border-t border-border bg-card mobile:grid"
    >
      {mobileTabs.map((tab) => {
        const Icon = tab.icon
        const on = tab.key === current
        const inner = (
          <>
            <Icon size={20} aria-hidden="true" />
            <span className={cn(on && 'shadow-[inset_0_-2px_0_var(--signal)]')}>{t(tab.labelKey)}</span>
          </>
        )
        if (tab.to) {
          return (
            <Link
              key={tab.key}
              to={tab.to}
              aria-current={on ? 'page' : undefined}
              className={cn(tabClass, on ? 'text-foreground' : 'text-muted-foreground')}
            >
              {inner}
            </Link>
          )
        }
        return (
          <DropdownMenu key={tab.key}>
            <DropdownMenuTrigger className={cn(tabClass, 'outline-none', on ? 'text-foreground' : 'text-muted-foreground')}>
              {inner}
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" side="top" className="min-w-48">
              <DropdownMenuLabel className="font-mono text-[11px] lowercase text-muted-foreground">{t('nav.more')}</DropdownMenuLabel>
              {moreLinks.map((l) => (
                <DropdownMenuItem key={l.to} onSelect={() => navigate(l.to)}>
                  {t(l.labelKey)}
                </DropdownMenuItem>
              ))}
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => setThemeChoice(darkNow ? 'light' : 'dark')}>
                {darkNow ? <Sun size={14} /> : <Moon size={14} />}
                {darkNow ? t('shell.theme.toLight') : t('shell.theme.toDark')}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => void setLanguage(i18n, i18n.language.startsWith('zh') ? 'en' : 'zh')}>
                {t('shell.language.label')}：{i18n.language.startsWith('zh') ? 'EN' : '中'}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => void onLogout()}>
                <LogOut size={14} />
                {t('shell.logout')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )
      })}
    </nav>
  )
}
