import { createBrowserRouter, RouterProvider } from 'react-router'
import { AppRoutes } from './routes'
import { SessionProvider } from './session'

// 使用数据路由（createBrowserRouter）是为了支持 useBlocker：设置页在有未保存修改时拦截路由切换。
// 所有页面仍由 AppRoutes 里的 <Routes> 描述，这里只用一条通配路由承载。
const router = createBrowserRouter([
  {
    path: '*',
    element: (
      <SessionProvider>
        <AppRoutes />
      </SessionProvider>
    ),
  },
])

export function App() {
  return <RouterProvider router={router} />
}
