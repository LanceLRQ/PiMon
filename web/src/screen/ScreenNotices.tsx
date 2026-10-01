import { CloudOff, KeyRound, MonitorSmartphone } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router'
import { ThemeRoot } from '@/templates'
import { readStoredScreenTheme } from './theme-apply'

/** 屏幕端整页提示的外框：沿用上次的屏幕主题，全屏居中，不滚动 */
export function ShellFrame({ children }: { children?: ReactNode }) {
  return (
    <ThemeRoot
      themeId={readStoredScreenTheme()}
      className="fixed inset-0 z-40 flex flex-col items-center justify-center gap-4 overflow-hidden bg-s-bg p-8 text-center text-s-fg"
    >
      {children}
    </ThemeRoot>
  )
}

function Notice({ icon, title, body, children }: { icon: ReactNode; title: string; body: string; children?: ReactNode }) {
  return (
    <ShellFrame>
      <div className="text-s-muted-fg" aria-hidden>
        {icon}
      </div>
      <h1 className="text-3xl font-semibold">{title}</h1>
      <p className="max-w-xl text-lg text-s-muted-fg">{body}</p>
      {children}
    </ShellFrame>
  )
}

/** 屏幕令牌失效（没有会话、会话被吊销）：让人去树莓派上重启 kiosk */
export function TokenInvalidPage() {
  const { t } = useTranslation()
  return <Notice icon={<KeyRound size={56} />} title={t('screenApp.tokenInvalid.title')} body={t('screenApp.tokenInvalid.body')} />
}

/** hub 不可达且没有可显示的缓存数据 */
export function HubDownPage() {
  const { t } = useTranslation()
  return <Notice icon={<CloudOff size={56} />} title={t('screenApp.hubDown.title')} body={t('screenApp.hubDown.body')} />
}

/** 管理员会话打开 /screen：仅提示，不连接中枢，也不会被当作显示器 */
export function AdminPreviewPage() {
  const { t } = useTranslation()
  return (
    <Notice icon={<MonitorSmartphone size={56} />} title={t('screenApp.adminPreview.title')} body={t('screenApp.adminPreview.body')}>
      <Link to="/" className="text-lg text-s-primary underline">
        {t('screenApp.adminPreview.back')}
      </Link>
    </Notice>
  )
}
