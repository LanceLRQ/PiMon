import { describe, expect, it } from 'vitest'
import type { AgentInfo, PairingResult } from '@/types/generated'
import { device } from '../test-utils'
import { findPairedDevice, formatCountdown, listenInstallCommand, nameTooLong, pairingCommands, releaseDownloadCommand } from './pairing'

const result: PairingResult = {
  code: 'ABCD-1234',
  expires_at: '2026-10-04T10:15:00Z',
  urls: ['wss://192.168.1.10:31418', 'wss://pimon.home.lan:31418'],
  fingerprint: 'SHA256:fp',
  command: 'curl -fsSL http://192.168.1.10:31415/agent/install.sh | sh -s -- --hub wss://192.168.1.10:31418 --fp SHA256:fp --code ABCD-1234',
  steps: [
    'PLATFORM=x',
    'curl -fsSLo pimon-agent http://192.168.1.10:31415/agent/download/$PLATFORM',
    'echo "$(curl ...)" | (command -v sha256sum >/dev/null && sha256sum -c - || shasum -a 256 -c -)',
    '$([ "$(uname -s)" = Darwin ] || echo sudo) ./pimon-agent install --hub wss://192.168.1.10:31418 --fp SHA256:fp --code ABCD-1234',
  ],
}

describe('pairingCommands', () => {
  it('换 hub 地址只改 --hub，下载地址不动', () => {
    const c = pairingCommands(result, 'wss://pimon.home.lan:31418', 'server')
    expect(c.oneLine).toContain('--hub wss://pimon.home.lan:31418')
    expect(c.oneLine).not.toContain('wss://192.168.1.10:31418')
    expect(c.oneLine).toContain('http://192.168.1.10:31415/agent/install.sh')
    expect(c.steps[1]).toContain('http://192.168.1.10:31415/agent/download')
    expect(c.steps[3]).toContain('--hub wss://pimon.home.lan:31418')
    expect(c.steps[3]).not.toContain('192.168.1.10')
  })

  it('开发机最后一步不带 sudo，其他角色保留', () => {
    expect(pairingCommands(result, result.urls[0], 'dev').steps[3]).toMatch(/^\.\/pimon-agent install /)
    expect(pairingCommands(result, result.urls[0], 'nas').steps[3]).toContain('echo sudo')
  })
})

describe('findPairedDevice', () => {
  const issuedAt = Date.parse('2026-10-04T10:00:00Z')
  const fresh = (id: string, extra = {}) => device(id, { created_at: '2026-10-04T10:01:00Z', ...extra })

  it('只认基线之外、拨入、创建时间不早于签发时间的设备', () => {
    const devs = [device('old'), fresh('a', { direction: 'dial_out' }), fresh('b', { created_at: '2026-10-04T09:59:00Z' }), fresh('c')]
    expect(findPairedDevice(devs, new Set(['old']), issuedAt, '')?.id).toBe('c')
    expect(findPairedDevice(devs.slice(0, 3), new Set(['old']), issuedAt, '')).toBeNull()
    expect(findPairedDevice(null, new Set(), issuedAt, '')).toBeNull()
  })

  it('同时出现多个时优先名字以所填名称开头的', () => {
    const devs = [fresh('a', { name: '别的机器' }), fresh('b', { name: '飞牛 NAS 2' })]
    expect(findPairedDevice(devs, new Set(), issuedAt, '飞牛')?.id).toBe('b')
    expect(findPairedDevice(devs, new Set(), issuedAt, '')?.id).toBe('a')
  })
})

describe('其他工具', () => {
  it('倒计时格式与上限', () => {
    expect(formatCountdown(14 * 60_000 + 52_000)).toBe('14:52')
    expect(formatCountdown(-5)).toBe('00:00')
  })

  it('名称按字符数（不是字节）算 64 个上限', () => {
    expect(nameTooLong('飞'.repeat(64))).toBe(false)
    expect(nameTooLong('飞'.repeat(65))).toBe(true)
  })

  const info = {
    release_url_template: 'https://github.com/x/y/releases/download/v1.2.3/pimon-agent-<os>-<arch>',
    release_checksums: { 'linux-amd64': 'abc123' },
  } as unknown as AgentInfo

  it('Releases 下载命令：有校验和用校验和，没有则取 SHA256SUMS；开发构建返回 null', () => {
    const a = releaseDownloadCommand(info, 'linux-amd64')!
    expect(a).toContain('PLATFORM=linux-amd64')
    expect(a).toContain('releases/download/v1.2.3/pimon-agent-$PLATFORM')
    expect(a).toContain('abc123  pimon-agent-$PLATFORM')
    expect(releaseDownloadCommand(info, 'linux-arm64')).toContain('/v1.2.3/SHA256SUMS')
    expect(releaseDownloadCommand({ ...info, release_url_template: '' }, 'linux-amd64')).toBeNull()
  })

  it('监听安装命令带端口与 pair', () => {
    expect(listenInstallCommand(true)).toContain('install --listen :31418')
    expect(listenInstallCommand(false)).toContain('chmod +x pimon-agent\n')
    expect(listenInstallCommand(true)).toContain('sudo pimon-agent pair')
  })
})
