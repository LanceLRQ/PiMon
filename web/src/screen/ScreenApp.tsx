import { useEffect, useState } from 'react'
// 三套主题 token 与屏幕端样式只在屏幕根模块引入，不进 main.tsx（管理端与屏幕端共用 index.html，全局引入会串色）
import '@/themes/index.css'
import './screen.css'
import { ScreenView } from './ScreenView'
import { screenStore } from './screen-store'
import { createScreenSession } from './session'
import { ScreenNavigator } from './state-machine'

/** 屏幕端应用：连接中枢、驱动状态机并渲染屏幕。路由守卫与断线、令牌失效等外壳由 D4b 补充。 */
export function ScreenApp() {
  const [nav] = useState(() => new ScreenNavigator())
  const [session] = useState(() => createScreenSession({ store: screenStore, nav }))
  useEffect(() => {
    session.start()
    return () => {
      session.stop()
      nav.dispose()
      screenStore.reset()
    }
  }, [session, nav])
  return <ScreenView store={screenStore} nav={nav} reporter={session.reporter} />
}
