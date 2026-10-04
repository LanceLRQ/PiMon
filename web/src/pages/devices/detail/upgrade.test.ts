import { describe, expect, it } from 'vitest'
import type { Proxy } from '@/types/generated'
import { device } from '../test-utils'
import { proxyUrl, upgradeCommand } from './upgrade'

const socks = { id: 'p1', name: 'HK', scheme: 'socks5', address: '10.0.0.2:1080', remote_dns: true } as Proxy

describe('upgradeCommand', () => {
  it('拨入 Linux 系统模式带 sudo，darwin 用户模式不带', () => {
    expect(upgradeCommand(device('a'), 'v1.2.3', null)).toEqual({ kind: 'command', text: 'sudo pimon-agent upgrade', proxyHint: null })
    expect(upgradeCommand(device('a', { os: 'darwin' }), 'v1.2.3', null)).toEqual({ kind: 'command', text: 'pimon-agent upgrade', proxyHint: null })
  })

  it('拨入设备不依赖中枢版本，开发构建也给命令', () => {
    expect(upgradeCommand(device('a'), 'dev', null)).toMatchObject({ kind: 'command', text: 'sudo pimon-agent upgrade' })
    expect(upgradeCommand(device('a'), null, null)).toMatchObject({ kind: 'command' })
  })

  it('拨出设备带 --release 中枢版本，有代理时附 --proxy 提示（不进命令本身）', () => {
    const d = device('v', { direction: 'dial_out', role: 'vps', proxy_id: 'p1' })
    expect(upgradeCommand(d, 'v1.2.3', socks)).toEqual({
      kind: 'command',
      text: 'sudo pimon-agent upgrade --release v1.2.3',
      proxyHint: 'socks5h://10.0.0.2:1080',
    })
    expect(upgradeCommand({ ...d, proxy_id: '' }, 'v1.2.3', null)).toMatchObject({ proxyHint: null })
  })

  it('拨出设备在中枢不是 vX.Y.Z 时不给命令', () => {
    const d = device('v', { direction: 'dial_out', role: 'vps' })
    for (const v of ['dev', 'v1.2', 'v1.2.3-rc1', '1.2.3', null]) {
      expect(upgradeCommand(d, v, null)).toEqual({ kind: 'noRelease' })
    }
  })
})

describe('proxyUrl', () => {
  it('socks5 在远端解析时写成 socks5h，其余原样', () => {
    expect(proxyUrl(socks)).toBe('socks5h://10.0.0.2:1080')
    expect(proxyUrl({ ...socks, remote_dns: false })).toBe('socks5://10.0.0.2:1080')
    expect(proxyUrl({ ...socks, scheme: 'http', remote_dns: true })).toBe('http://10.0.0.2:1080')
  })
})
