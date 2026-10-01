import { Outlet } from 'react-router'
import { MobileTabBar } from './MobileTabBar'
import { Sidebar } from './Sidebar'
import { useLiveConnection, useSettingsLanguage } from './live'

// 已登录页面的外壳：桌面是左侧栏 + 主区，手机（≤640px）是主区 + 底部标签栏。
// 主区自己滚动，整页不滚动。
export function AdminLayout() {
  useLiveConnection()
  useSettingsLanguage()
  return (
    <div className="flex h-screen w-screen overflow-hidden bg-background">
      <Sidebar />
      <main className="flex min-w-0 flex-1 flex-col overflow-y-auto mobile:pb-16">
        <Outlet />
      </main>
      <MobileTabBar />
    </div>
  )
}
