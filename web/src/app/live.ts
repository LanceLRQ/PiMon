import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { LiveSocket } from '@/api/ws'
import { applySettingsLanguage } from '@/i18n'
import { liveStore, selectSettings, useLiveStore } from '@/store/live-store'
import { useSession } from './session'

// 已登录页面的实时连接：进入时建立，离开（退出登录、跳走）时关闭。
// 握手阶段就失败多半是会话失效，重新查询会话交给守卫处理。
export function useLiveConnection() {
  const { refresh } = useSession()
  useEffect(() => {
    const socket = new LiveSocket({ store: liveStore, onHandshakeFailed: () => void refresh() })
    socket.start()
    return () => {
      socket.stop()
      liveStore.reset()
    }
  }, [refresh])
}

// 本浏览器没有语言覆盖时，界面语言跟随全局设置里的 language
export function useSettingsLanguage() {
  const { i18n } = useTranslation()
  const settings = useLiveStore(selectSettings)
  const language = settings?.language
  useEffect(() => {
    if (language) void applySettingsLanguage(i18n, language)
  }, [i18n, language])
}
