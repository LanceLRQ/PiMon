import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { json, mockApi, renderWithApp, seedStore } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { Proxy } from '@/types/generated'
import { device } from '../test-utils'
import { DialOutForm } from './DialOutForm'

beforeEach(() => liveStore.reset())

const proxies: Proxy[] = [
  { id: 'p1', name: 'HK-01', scheme: 'socks5h', address: '10.0.0.2:1080', remote_dns: true, location: 'any', auth: { set: false } } as unknown as Proxy,
]

describe('拨出重新配对的代理预填', () => {
  it('旧代理已被删除：下拉显示直连，提交的也是直连（空串）', async () => {
    const submit = vi.fn().mockResolvedValue(device('v2', { direction: 'dial_out' }))
    mockApi((req) => (req.method === 'GET' && req.url === '/api/proxies' ? json(200, proxies) : undefined))
    seedStore([], { devices: [] })
    await renderWithApp(
      <DialOutForm mode="repair" initial={{ address: 'wss://203.0.113.8:31418', fingerprint: 'SHA256:abc', proxy_id: 'gone' }} submit={submit} onViewDetail={() => {}} />,
    )
    const select = screen.getByLabelText('代理') as HTMLSelectElement
    await waitFor(() => expect(select.value).toBe(''))
    await userEvent.type(screen.getByLabelText(/^配对码/), '5ZHM-1T8Q')
    await userEvent.click(screen.getByRole('button', { name: '重新配对' }))
    await waitFor(() => expect(submit).toHaveBeenCalled())
    expect(submit.mock.calls[0][0].proxy_id).toBe('')
  })

  it('旧代理仍存在时保持预填', async () => {
    mockApi((req) => (req.method === 'GET' && req.url === '/api/proxies' ? json(200, proxies) : undefined))
    seedStore([], { devices: [] })
    await renderWithApp(<DialOutForm mode="repair" initial={{ address: 'wss://203.0.113.8:31418', proxy_id: 'p1' }} onViewDetail={() => {}} />)
    const select = screen.getByLabelText('代理') as HTMLSelectElement
    await waitFor(() => expect(select.value).toBe('p1'))
  })
})
