import { lazy, Suspense, type ComponentType, type ReactNode } from 'react'
import { Route, Routes } from 'react-router'
import { AdminLayout } from './AdminLayout'
import { NotFoundPage } from './NotFoundPage'
import { PublicRoute, RequireSession } from './guard'
import { LazyBoundary, RouteFallback } from './route-fallback'

// 页面按路由拆成独立代码块，首屏只下载外壳与当前页。
// 各页面文件用具名导出，这里按名字取出并适配成 React.lazy 需要的默认导出。
function page<M>(load: () => Promise<M>, pick: (module: M) => ComponentType) {
  const Page = lazy(async () => ({ default: pick(await load()) }))
  return function LazyPage(): ReactNode {
    return (
      <LazyBoundary>
        <Suspense fallback={<RouteFallback />}>
          <Page />
        </Suspense>
      </LazyBoundary>
    )
  }
}

const InstanceEditPage = page(() => import('@/pages/instances/InstanceEditPage'), (m) => m.InstanceEditPage)
const InstanceNewPage = page(() => import('@/pages/instances/InstanceNewPage'), (m) => m.InstanceNewPage)
const InstancesPage = page(() => import('@/pages/instances/InstancesPage'), (m) => m.InstancesPage)
const LoginPage = page(() => import('@/pages/login/LoginPage'), (m) => m.LoginPage)
const OverviewPage = page(() => import('@/pages/overview/OverviewPage'), (m) => m.OverviewPage)
const ProxiesPage = page(() => import('@/pages/proxies/ProxiesPage'), (m) => m.ProxiesPage)
const ScreensPage = page(() => import('@/pages/screens/ScreensPages'), (m) => m.ScreensPage)
const LayoutEditorPage = page(() => import('@/pages/screens/ScreensPages'), (m) => m.LayoutEditorPage)
const SchedulePage = page(() => import('@/pages/screens/ScreensPages'), (m) => m.SchedulePage)
const RemotePage = page(() => import('@/pages/screens/ScreensPages'), (m) => m.RemotePage)
const SettingsPage = page(() => import('@/pages/settings/SettingsPage'), (m) => m.SettingsPage)
const ScreenPage = page(() => import('@/screen/ScreenGate'), (m) => m.ScreenGate)
const SetupPage = page(() => import('@/pages/setup/SetupPage'), (m) => m.SetupPage)
const SystemPage = page(() => import('@/pages/system/SystemPage'), (m) => m.SystemPage)

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
      {/* 屏幕端应用：自带守卫（令牌失效页、设置码页、管理员预览提示），不走 RequireSession，也不带管理外壳 */}
      <Route path="screen/*" element={<ScreenPage />} />
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
