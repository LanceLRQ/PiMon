import type { ScreenControl } from '@/types/protocol.generated'
import type { ScreenNavigator } from './state-machine'

export interface ControlDeps {
  nav: Pick<ScreenNavigator, 'remoteSwitch'>
  reload: () => void
}

/** 处理服务端下发的一次性屏幕指令；开屏、关屏、临时亮屏不走这里，随 screen_state 下发 */
export function handleScreenControl(msg: ScreenControl, { nav, reload }: ControlDeps) {
  switch (msg.action) {
    case 'refresh':
      reload()
      break
    case 'switch':
      if (msg.screen_id) nav.remoteSwitch(msg.screen_id)
      break
    default:
      break
  }
}
