import { describe, expect, it, vi } from 'vitest'
import type { ScreenControl } from '@/types/protocol.generated'
import { handleScreenControl } from './control'

const msg = (action: string, screen_id?: string): ScreenControl => ({
  type: 'screen_control',
  server_time: '2026-10-01T00:00:00Z',
  action,
  screen_id,
  op_id: 1,
})

describe('screen_control 指令', () => {
  it('refresh 整页刷新', () => {
    const reload = vi.fn()
    const nav = { remoteSwitch: vi.fn() }
    handleScreenControl(msg('refresh'), { nav, reload })
    expect(reload).toHaveBeenCalledTimes(1)
    expect(nav.remoteSwitch).not.toHaveBeenCalled()
  })

  it('switch 以远程命令的优先级切到指定 screen', () => {
    const reload = vi.fn()
    const nav = { remoteSwitch: vi.fn() }
    handleScreenControl(msg('switch', 's2'), { nav, reload })
    expect(nav.remoteSwitch).toHaveBeenCalledWith('s2')
    expect(reload).not.toHaveBeenCalled()
  })

  it('switch 缺少 screen_id 与未知动作被忽略', () => {
    const reload = vi.fn()
    const nav = { remoteSwitch: vi.fn() }
    handleScreenControl(msg('switch'), { nav, reload })
    handleScreenControl(msg('explode'), { nav, reload })
    expect(nav.remoteSwitch).not.toHaveBeenCalled()
    expect(reload).not.toHaveBeenCalled()
  })
})
