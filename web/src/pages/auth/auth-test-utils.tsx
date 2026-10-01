import { render } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter, useLocation } from 'react-router'
import { vi } from 'vitest'
import { AppRoutes } from '@/app/routes'
import { SessionProvider } from '@/app/session'
import type { SessionInfo } from '@/api/session'
import { createI18n, type Language } from '@/i18n'

export class IdleSocket {
  static OPEN = 1
  readyState = 0
  onopen = null
  onmessage = null
  onclose = null
  onerror = null
  send() {}
  close() {}
}

export function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

export function apiError(status: number, code: string, details: Record<string, unknown> = {}) {
  return json(status, { error: { code, details } })
}

function Probe() {
  const l = useLocation()
  return <div data-testid="loc">{l.pathname + l.search}</div>
}

export interface Call {
  url: string
  method: string
  body: Record<string, unknown> | null
}

export type Handler = (call: Call) => Response | Promise<Response> | undefined

// 渲染完整路由，按 handler 应答 fetch；/api/session 由 session 函数给出（可随调用次数变化）
export async function mountAuth(opts: { path: string; session: () => SessionInfo; handler?: Handler; lng?: Language }) {
  const calls: Call[] = []
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const call: Call = {
      url,
      method: init?.method ?? 'GET',
      body: init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : null,
    }
    calls.push(call)
    if (url === '/api/session') return json(200, opts.session())
    const r = await opts.handler?.(call)
    return r ?? json(404, { error: { code: 'not_found', details: {} } })
  })
  vi.stubGlobal('fetch', fetchMock)
  vi.stubGlobal('WebSocket', IdleSocket)
  const i18n = await createI18n(opts.lng ?? 'zh')
  render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={[opts.path]}>
        <SessionProvider>
          <AppRoutes />
          <Probe />
        </SessionProvider>
      </MemoryRouter>
    </I18nextProvider>,
  )
  return { calls, i18n }
}
