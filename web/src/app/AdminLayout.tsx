import { useTranslation } from 'react-i18next'
import { Outlet } from 'react-router'
import { selectBuildOutdated, useLiveStore } from '@/store/live-store'
import { MobileTabBar } from './MobileTabBar'
import { ToastProvider } from '@/ui/toast'
import { Sidebar } from './Sidebar'
import { useLiveConnection, useSettingsLanguage } from './live'

// 已登录页面的外壳：桌面是左侧栏 + 主区，手机（≤640px）是主区 + 底部标签栏。
// 主区自己滚动，整页不滚动。
export function AdminLayout() {
  useLiveConnection()
  useSettingsLanguage()
  const { t } = useTranslation()
  const outdated = useLiveStore(selectBuildOutdated)
  return (
    <ToastProvider>
      <div className="flex h-screen w-screen overflow-hidden bg-background">
        <Sidebar />
        <main className="flex min-w-0 flex-1 flex-col overflow-y-auto mobile:pb-16">
          {outdated && (
            <div role="status" className="border-b border-border bg-signal-soft px-6 py-2 text-[13px] mobile:px-3.5">
              {t('shell.versionUpdated')}
            </div>
          )}
          <Outlet />
        </main>
        <MobileTabBar />
      </div>
    </ToastProvider>
  )
}
