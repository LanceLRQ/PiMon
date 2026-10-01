import { readFileSync } from 'node:fs'
import path from 'node:path'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// sw.js 是不经打包的经典脚本：读源码，在假的 self 里求值，取出其中的纯函数来测。
interface Deps {
  fetch: (req: unknown) => Promise<Response>
  caches: { open(name: string): Promise<FakeCache> }
  origin: string
}
interface FakeCache {
  match(key: unknown): Promise<Response | undefined>
  put(key: unknown, res: Response): Promise<void>
}
interface SwApi {
  handleFetch(req: unknown, deps: Deps): Promise<Response> | null
  SHELL_CACHE: string
}

const source = readFileSync(path.resolve(import.meta.dirname, '../../public/screen/sw.js'), 'utf8')
const sw = new Function('self', `${source}\nreturn { handleFetch, SHELL_CACHE }`)({}) as SwApi

const origin = 'http://hub.local:31415'

function req(url: string, over: Record<string, unknown> = {}) {
  return { url: origin + url, method: 'GET', mode: 'same-origin', ...over }
}
const nav = (url: string) => req(url, { mode: 'navigate' })

function makeCaches() {
  const store = new Map<string, Response>()
  const keyOf = (k: unknown) => (typeof k === 'string' ? k : (k as { url: string }).url)
  const cache: FakeCache = {
    async match(key) {
      return store.get(keyOf(key))?.clone()
    },
    async put(key, res) {
      store.set(keyOf(key), res)
    },
  }
  const caches: Deps['caches'] = { open: async () => cache }
  return { caches, store }
}

function html(body: string, status = 200) {
  return new Response(body, { status, headers: { 'Content-Type': 'text/html' } })
}

let env: ReturnType<typeof makeCaches>
const fetchMock = vi.fn<(r: unknown) => Promise<Response>>()
const deps = (): Deps => ({ fetch: fetchMock, caches: env.caches, origin })

beforeEach(() => {
  env = makeCaches()
  fetchMock.mockReset()
})

describe('Service Worker：路由', () => {
  it('不处理非 GET、跨源、/screen/auth、管理页与接口请求', () => {
    expect(sw.handleFetch(req('/screen', { method: 'POST', mode: 'navigate' }), deps())).toBeNull()
    expect(sw.handleFetch({ url: 'http://other.example/assets/a.js', method: 'GET', mode: 'cors' }, deps())).toBeNull()
    expect(sw.handleFetch(nav('/screen/auth?token=x'), deps())).toBeNull()
    expect(sw.handleFetch(nav('/screens'), deps())).toBeNull()
    expect(sw.handleFetch(nav('/settings'), deps())).toBeNull()
    expect(sw.handleFetch(req('/api/session'), deps())).toBeNull()
    expect(sw.handleFetch(req('/ws'), deps())).toBeNull()
    expect(sw.handleFetch(req('/favicon.svg'), deps())).toBeNull()
  })

  it('处理 /screen 导航与 /assets/ 资源', () => {
    fetchMock.mockResolvedValue(html('x'))
    expect(sw.handleFetch(nav('/screen'), deps())).not.toBeNull()
    expect(sw.handleFetch(nav('/screen/'), deps())).not.toBeNull()
    expect(sw.handleFetch(req('/assets/index-abc.js'), deps())).not.toBeNull()
  })
})

describe('Service Worker：导航 network-first', () => {
  it('在线时返回网络内容并更新缓存的外壳', async () => {
    fetchMock.mockResolvedValue(html('新外壳'))
    const res = await sw.handleFetch(nav('/screen'), deps())!
    expect(await res.text()).toBe('新外壳')
    expect(await (await env.caches.open('x').then((c) => c.match('/screen')))!.text()).toBe('新外壳')
  })

  it('离线时回落到缓存的外壳（任何 /screen 子路径共用一份）', async () => {
    fetchMock.mockResolvedValueOnce(html('旧外壳'))
    await sw.handleFetch(nav('/screen'), deps())!
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'))
    const res = await sw.handleFetch(nav('/screen/anything'), deps())!
    expect(res.status).toBe(200)
    expect(await res.text()).toBe('旧外壳')
  })

  it('网络返回 5xx（hub 正在重启、反代 502）时同样回落缓存', async () => {
    fetchMock.mockResolvedValueOnce(html('旧外壳'))
    await sw.handleFetch(nav('/screen'), deps())!
    fetchMock.mockResolvedValue(html('bad gateway', 502))
    const res = await sw.handleFetch(nav('/screen'), deps())!
    expect(await res.text()).toBe('旧外壳')
  })

  it('网络 5xx 且没有缓存时原样返回；错误响应不写入缓存', async () => {
    fetchMock.mockResolvedValue(html('bad gateway', 502))
    const res = await sw.handleFetch(nav('/screen'), deps())!
    expect(res.status).toBe(502)
    expect(env.store.size).toBe(0)
  })

  it('离线且没有缓存：返回自带的「hub 未运行」页，不让浏览器出错误页', async () => {
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'))
    const res = await sw.handleFetch(nav('/screen'), deps())!
    expect(res.status).toBe(503)
    expect(res.headers.get('Content-Type')).toContain('text/html')
    const text = await res.text()
    expect(text).toContain('hub 未运行')
    expect(text).toContain('not running')
  })
})

describe('Service Worker：/assets/* cache-first', () => {
  it('命中缓存不访问网络', async () => {
    fetchMock.mockResolvedValueOnce(new Response('js-body'))
    await sw.handleFetch(req('/assets/a-1.js'), deps())!
    fetchMock.mockClear()
    const res = await sw.handleFetch(req('/assets/a-1.js'), deps())!
    expect(await res.text()).toBe('js-body')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('未命中时取网络并写缓存，失败的响应不缓存', async () => {
    fetchMock.mockResolvedValueOnce(new Response('missing', { status: 404 }))
    const bad = await sw.handleFetch(req('/assets/b.js'), deps())!
    expect(bad.status).toBe(404)
    expect(env.store.size).toBe(0)
    fetchMock.mockResolvedValueOnce(new Response('ok'))
    await sw.handleFetch(req('/assets/b.js'), deps())!
    expect(env.store.size).toBe(1)
  })

  it('离线且未缓存：请求失败（由浏览器处理），不吞成假成功', async () => {
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'))
    await expect(sw.handleFetch(req('/assets/c.js'), deps())!).rejects.toBeInstanceOf(TypeError)
  })
})
