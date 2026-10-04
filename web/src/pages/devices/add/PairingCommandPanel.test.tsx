import { screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { mockApi, renderWithApp } from '@/pages/instances/test-utils'
import type { PairingResult } from '@/types/generated'
import { device } from '../test-utils'
import { PairingCommandPanel } from './PairingCommandPanel'

afterEach(() => vi.unstubAllGlobals())

const t0 = Date.parse('2026-10-04T00:00:00Z')
function result(expiresMs: number): PairingResult {
  return {
    code: 'ABCD-EFGH', expires_at: new Date(expiresMs).toISOString(), urls: ['wss://hub:31418'], fingerprint: 'SHA256:fp',
    command: 'curl x | sh -s -- --hub wss://hub:31418', steps: ['a', 'b', 'c', 'd'],
  }
}

async function mount(props: Partial<Parameters<typeof PairingCommandPanel>[0]>) {
  mockApi()
  await renderWithApp(<PairingCommandPanel mode="add" result={result(t0 + 15 * 60_000)} role="server" info={null} now={t0} connected={null} onViewDetail={() => {}} {...props} />)
}

describe('PairingCommandPanel', () => {
  it('倒计时封顶 15 分钟：到期时间比推算的签发时间晚一点也显示 15:00', async () => {
    await mount({ result: result(t0 + 15 * 60_000 + 1000) })
    expect(screen.getByText('配对码剩余 15:00')).toBeInTheDocument()
  })

  it('add 模式过期提示「返回修改」', async () => {
    await mount({ result: result(t0 - 1000) })
    expect(screen.getAllByText(/返回修改/).length).toBeGreaterThan(0)
  })

  it('repair 模式的文案不引用「返回修改」', async () => {
    await mount({ mode: 'repair', result: result(t0 - 1000) })
    expect(screen.queryByText(/返回修改/)).toBeNull()
    expect(screen.getByText(/点「重新生成」获取新的配对码/)).toBeInTheDocument()
    expect(screen.getByText(/过期后点「重新生成」/)).toBeInTheDocument()
  })

  it('已找到本次配对的设备但它不兼容：显示对应状态，不再显示等待连接', async () => {
    const found = device('d9', { name: '书房', presence: 'incompatible', confirmed: false, incompatible: { reason: 'version_too_old', required_min: 3 } })
    await mount({ found })
    expect(screen.getByText(/已找到本次配对的设备「书房」/)).toBeInTheDocument()
    expect(screen.queryByText(/等待 agent 连接/)).toBeNull()
  })

  it('找到的占位还在等待握手（pending）时仍显示等待连接', async () => {
    await mount({ found: device('d9', { presence: 'pending', confirmed: false }) })
    expect(screen.getByText(/等待 agent 连接/)).toBeInTheDocument()
    expect(screen.queryByText(/已找到本次配对的设备/)).toBeNull()
  })
})
