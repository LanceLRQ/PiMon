import { useEffect, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate, Outlet, useLocation, useSearchParams } from 'react-router'
import { Button } from '@/ui/button'
import { useSession } from './session'

// 登录后回跳的目标只接受站内路径，且不能回到登录/首次设置页本身；
// 浏览器会把反斜杠当作斜杠、并忽略 tab 与换行，所以含反斜杠或空白字符的一律回根
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith('/') || next.startsWith('//') || /[\\\s]/.test(next)) return '/'
  if (next === '/login' || next === '/setup' || next.startsWith('/login?') || next.startsWith('/setup?')) return '/'
  return next
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="grid min-h-screen place-items-center bg-background p-4 text-sm text-muted-foreground">{children}</div>
}

// 会话尚未确定时的占位，避免登录页或受保护页面闪现
function SessionPending() {
  const { t } = useTranslation()
  const { status, refresh } = useSession()
  if (status === 'error') {
    return (
      <Centered>
        <div className="flex flex-col items-center gap-3" role="alert">
          <p>{t('shell.sessionError')}</p>
          <Button variant="outline" size="sm" onClick={() => void refresh()}>
            {t('shell.retry')}
          </Button>
        </div>
      </Centered>
    )
  }
  return (
    <Centered>
      <span role="status">{t('shell.loading')}</span>
    </Centered>
  )
}

// 受保护页面的守卫：需要首次设置 → /setup；未登录 → /login 并记住原页面；屏幕会话 → /screen
export function RequireSession() {
  const { status, info, redirectExternal } = useSession()
  const location = useLocation()
  const isScreen = status === 'ready' && info?.authenticated === true && info.kind === 'screen'
  // 已经在屏幕端路径下就不再跳转，否则服务端回退返回管理端页面会造成无限刷新（/screens 是管理页，不算）
  const onScreenPath = location.pathname === '/screen' || location.pathname.startsWith('/screen/')
  const shouldLeave = isScreen && !onScreenPath

  useEffect(() => {
    if (shouldLeave) redirectExternal('/screen')
  }, [shouldLeave, redirectExternal])

  if (status !== 'ready' || !info) return <SessionPending />
  if (info.needs_setup) return <Navigate to="/setup" replace />
  if (!info.authenticated) {
    const here = location.pathname + location.search
    const to = here === '/' ? '/login' : `/login?next=${encodeURIComponent(here)}`
    return <Navigate to={to} replace />
  }
  if (shouldLeave) return null
  return <Outlet />
}

// 登录页与首次设置页的守卫：按会话状态把用户送到该去的页面
export function PublicRoute({ page }: { page: 'login' | 'setup' }) {
  const { status, info } = useSession()
  const [params] = useSearchParams()
  if (status !== 'ready' || !info) return <SessionPending />
  if (page === 'login') {
    if (info.needs_setup) return <Navigate to="/setup" replace />
    if (info.authenticated && info.kind === 'admin') return <Navigate to={safeNext(params.get('next'))} replace />
  } else if (!info.needs_setup) {
    return <Navigate to={info.authenticated && info.kind === 'admin' ? '/' : '/login'} replace />
  }
  return <Outlet />
}
