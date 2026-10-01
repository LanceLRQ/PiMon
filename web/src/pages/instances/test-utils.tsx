import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter, useLocation } from 'react-router'
import { vi } from 'vitest'
import { createI18n, type Language } from '@/i18n'
import { liveStore } from '@/store/live-store'
import type { Instance, PluginList } from '@/types/generated'
import { ToastProvider } from '@/ui/toast'

export interface Req {
  method: string
  // 含查询串的请求路径
  url: string
  body: unknown
}

export type ApiHandler = (req: Req) => Response | Promise<Response> | undefined

export function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

export function apiError(status: number, code: string, details: Record<string, unknown> = {}): Response {
  return json(status, { error: { code, details } })
}

export const defaultPlugins: PluginList = {
  plugins: [
    {
      id: 'demo',
      version: '1.0.0',
      name: '演示数据',
      kind: 'source',
      runtime: 'builtin',
      origin: 'builtin',
      runs_on: ['hub'],
      interval_seconds: 30,
      min_interval_seconds: 0,
      timeout_seconds: 10,
      config_schema: [],
      outputs: [
        { key: 'cpu.pi', type: 'gauge', title: '树莓派 CPU' },
        { key: 'host[*]', type: 'state', title: '主机' },
      ],
      widgets: [],
    },
  ],
  errors: [],
  conflicts: [],
  plugin_dir: '/var/lib/pimon/plugins',
}

// 安装 fetch 替身：先让 handler 应答，没处理的请求中 /api/plugins 与 /api/backups 给默认值，其余 404
export function mockApi(handler: ApiHandler = () => undefined) {
  const calls: Req[] = []
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const req: Req = { method: init?.method ?? 'GET', url, body: init?.body ? JSON.parse(String(init.body)) : null }
    calls.push(req)
    const custom = await handler(req)
    if (custom) return custom
    const path = url.split('?')[0]
    if (req.method === 'GET' && path === '/api/plugins') return json(200, defaultPlugins)
    if (req.method === 'GET' && path === '/api/backups') return json(200, [])
    return apiError(404, 'not_found')
  })
  vi.stubGlobal('fetch', fetchMock)
  return { calls, fetchMock }
}

export function Probe() {
  const l = useLocation()
  return <div data-testid="loc">{l.pathname}</div>
}

export async function renderWithApp(ui: ReactElement, opts: { lng?: Language; route?: string } = {}) {
  const i18n = await createI18n(opts.lng ?? 'zh')
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter initialEntries={[opts.route ?? '/']}>
        <ToastProvider>
          {ui}
          <Probe />
        </ToastProvider>
      </MemoryRouter>
    </I18nextProvider>,
  )
}

export function seedStore(instances: Instance[], extra: { build?: string; connected?: boolean } = {}) {
  liveStore.applySnapshot({
    type: 'snapshot',
    build: extra.build ?? 'test-build',
    role: 'admin',
    topics: [],
    server_time: new Date().toISOString(),
    instances,
  } as unknown as Parameters<typeof liveStore.applySnapshot>[0])
  liveStore.setConnected(extra.connected ?? true)
}

export function patchInstance(inst: Instance) {
  liveStore.applyPatch({
    type: 'patch',
    entity: 'instance_state',
    server_time: new Date().toISOString(),
    instance: inst,
  } as unknown as Parameters<typeof liveStore.applyPatch>[0])
}
