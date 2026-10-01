import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
// 三套主题 token 与屏幕端样式只在屏幕根模块引入，不进 main.tsx
import '@/themes/index.css'
import './screen.css'
import { http } from '@/api/client'
import { LiveSocket, type LiveSocketOptions } from '@/api/ws'
import { useSession } from '@/app/session'
import { ThemeRoot } from '@/templates'
import type { ResolvedLayout } from '@/types/generated'
import { PreviewSink, previewTopics } from './admin-preview'
import { ScreenView } from './ScreenView'
import { createScreenStore } from './screen-store'
import { ShellFrame } from './ScreenNotices'
import { ScreenNavigator } from './state-machine'
import { readStoredScreenTheme } from './theme-apply'

const loadResolved = () => http.get<ResolvedLayout>('/api/screens/resolved')

export interface AdminScreenPreviewProps {
  /** 覆盖 LiveSocket 的选项（测试注入假 WebSocket） */
  socket?: Partial<LiveSocketOptions>
}

/** 顶部的「管理员预览」标识：窄条，不占屏幕的网格，只有返回链接可点 */
function PreviewBadge() {
  const { t } = useTranslation()
  return (
    <ThemeRoot
      themeId={readStoredScreenTheme()}
      className="pointer-events-none fixed inset-x-0 top-0 z-[60] flex items-center justify-center gap-3 bg-s-primary px-3 py-0.5 text-[12px] text-s-primary-fg"
    >
      <b data-testid="admin-preview-badge" className="font-semibold">
        {t('screenApp.adminPreview.badge')}
      </b>
      <span className="opacity-80">{t('screenApp.adminPreview.hint')}</span>
      <Link to="/" className="pointer-events-auto underline">
        {t('screenApp.adminPreview.back')}
      </Link>
    </ThemeRoot>
  )
}

function PreviewBody({ initial, socket: socketOptions }: { initial: ResolvedLayout } & AdminScreenPreviewProps) {
  const { refresh } = useSession()
  const [store] = useState(createScreenStore)
  const [nav] = useState(() => new ScreenNavigator())

  useEffect(() => {
    // 与屏幕会话的区别：不带 reporter（不上报 viewport、触摸能力与当前 screen），连上后额外订阅 screen_data
    const sink = new PreviewSink(store, loadResolved, initial)
    const socket: LiveSocket = new LiveSocket({
      store: sink,
      onHandshakeFailed: () => void refresh(),
      onConnected: () => socket.send({ type: 'subscribe', topics: previewTopics }),
      ...socketOptions,
    })
    socket.start()
    return () => {
      socket.stop()
      nav.dispose()
      store.reset()
    }
  }, [store, nav, initial, refresh, socketOptions])

  return (
    <>
      <ScreenView store={store} nav={nav} />
      <PreviewBadge />
    </>
  )
}

/**
 * 管理员会话打开 /screen：显示真实预览（解析后的布局加实时数据，轮播与屏幕一致）。
 * 它不是显示器：不上报 viewport，服务端也只把屏幕会话算作显示器在线。
 */
export function AdminScreenPreview(props: AdminScreenPreviewProps) {
  const { t } = useTranslation()
  const [resolved, setResolved] = useState<ResolvedLayout | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let cancelled = false
    loadResolved().then(
      (r) => !cancelled && setResolved(r),
      () => !cancelled && setFailed(true),
    )
    return () => {
      cancelled = true
    }
  }, [])

  if (!resolved) {
    return (
      <>
        <ShellFrame>
          <p role="status" className="text-lg text-s-muted-fg">
            {failed ? t('screenApp.adminPreview.failed') : t('screenApp.adminPreview.loading')}
          </p>
        </ShellFrame>
        <PreviewBadge />
      </>
    )
  }
  return <PreviewBody initial={resolved} {...props} />
}
