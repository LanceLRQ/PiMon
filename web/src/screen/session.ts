import { LiveSocket, type LiveSocketOptions } from '@/api/ws'
import { handleScreenControl } from './control'
import type { ScreenStore } from './screen-store'
import type { ScreenNavigator } from './state-machine'
import { ViewportReporter, readWindowViewport, windowCoarsePointer } from './viewport-reporter'

export interface ScreenSessionOptions {
  store: ScreenStore
  nav: ScreenNavigator
  reload?: () => void
  /** 覆盖 LiveSocket 的选项（测试注入假 WebSocket） */
  socket?: Partial<LiveSocketOptions>
}

/**
 * 屏幕端与中枢的会话：LiveSocket 连接、screen_control 处理、viewport 与当前 screen 上报。
 * 连接建立（含重连）时立即上报 viewport，窗口变化防抖后上报，亮屏 6 秒后补报由 reporter 负责。
 */
export function createScreenSession({ store, nav, reload = () => location.reload(), socket: socketOptions }: ScreenSessionOptions) {
  const reporter = new ViewportReporter({
    send: (m) => socket.send(m),
    read: readWindowViewport,
    coarse: windowCoarsePointer,
    onResize: (fn) => {
      window.addEventListener('resize', fn)
      return () => window.removeEventListener('resize', fn)
    },
  })
  const socket = new LiveSocket({
    store,
    onConnected: () => reporter.reportNow(),
    onScreenControl: (msg) => handleScreenControl(msg, { nav, reload }),
    ...socketOptions,
  })
  return {
    reporter,
    start() {
      reporter.start()
      socket.start()
    },
    stop() {
      socket.stop()
      reporter.stop()
    },
  }
}
