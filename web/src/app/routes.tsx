import { Route, Routes } from 'react-router'
import { InstanceEditPage } from '@/pages/instances/InstanceEditPage'
import { InstanceNewPage } from '@/pages/instances/InstanceNewPage'
import { InstancesPage } from '@/pages/instances/InstancesPage'
import { LoginPage } from '@/pages/login/LoginPage'
import { OverviewPage } from '@/pages/overview/OverviewPage'
import { ProxiesPage } from '@/pages/proxies/ProxiesPage'
import { LayoutEditorPage, RemotePage, SchedulePage, ScreensPage } from '@/pages/screens/ScreensPages'
import { SettingsPage } from '@/pages/settings/SettingsPage'
import { SetupPage } from '@/pages/setup/SetupPage'
import { SystemPage } from '@/pages/system/SystemPage'
import { AdminLayout } from './AdminLayout'
import { NotFoundPage } from './NotFoundPage'
import { PublicRoute, RequireSession } from './guard'

// 路由表：登录与首次设置是独立页（不带外壳），其余页面都在会话守卫与外壳之内
export function AppRoutes() {
  return (
    <Routes>
      <Route element={<PublicRoute page="login" />}>
        <Route path="/login" element={<LoginPage />} />
      </Route>
      <Route element={<PublicRoute page="setup" />}>
        <Route path="/setup" element={<SetupPage />} />
      </Route>
      <Route element={<RequireSession />}>
        <Route element={<AdminLayout />}>
          <Route index element={<OverviewPage />} />
          <Route path="screens" element={<ScreensPage />} />
          <Route path="screens/editor" element={<LayoutEditorPage />} />
          <Route path="screens/schedule" element={<SchedulePage />} />
          <Route path="screens/remote" element={<RemotePage />} />
          <Route path="instances" element={<InstancesPage />} />
          <Route path="instances/new" element={<InstanceNewPage />} />
          <Route path="instances/:id/edit" element={<InstanceEditPage />} />
          <Route path="proxies" element={<ProxiesPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="system" element={<SystemPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  )
}
