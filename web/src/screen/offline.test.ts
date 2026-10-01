import { describe, expect, it, vi } from 'vitest'
import { registerScreenServiceWorker } from './offline'

function fakeContainer() {
  return { register: vi.fn().mockResolvedValue({}), ready: Promise.resolve({}) } as never as ServiceWorkerContainer
}

function fakeCaches() {
  const store = new Map<string, Response>()
  const open = vi.fn(async () => ({
    match: async (k: string) => store.get(k),
    put: async (k: string, r: Response) => void store.set(k, r),
  }))
  return { caches: { open } as never as CacheStorage, store }
}

describe('registerScreenServiceWorker', () => {
  it('以 /screen 为作用域注册，并把已加载的 /assets/ 资源补进缓存', async () => {
    const sw = fakeContainer()
    const { caches, store } = fakeCaches()
    const fetchMock = vi.fn(async () => new Response('x'))
    await registerScreenServiceWorker({
      serviceWorker: sw,
      secure: true,
      caches,
      fetch: fetchMock as never,
      origin: 'http://hub:31415',
      resourceUrls: () => ['http://hub:31415/assets/a-1.js', 'http://hub:31415/assets/a-1.css', 'http://hub:31415/api/session', 'http://cdn.example/assets/x.js'],
    })
    expect(sw.register).toHaveBeenCalledWith('/screen/sw.js', { scope: '/screen' })
    expect([...store.keys()].sort()).toEqual(['http://hub:31415/assets/a-1.css', 'http://hub:31415/assets/a-1.js'])
  })

  it('非安全上下文或不支持 Service Worker 时静默跳过', async () => {
    const sw = fakeContainer()
    await registerScreenServiceWorker({ serviceWorker: sw, secure: false })
    expect(sw.register).not.toHaveBeenCalled()
    await expect(registerScreenServiceWorker({ serviceWorker: undefined, secure: true, caches: undefined })).resolves.toBeUndefined()
  })

  it('注册失败不抛出', async () => {
    const sw = { register: vi.fn().mockRejectedValue(new Error('SecurityError')), ready: Promise.resolve({}) } as never as ServiceWorkerContainer
    await expect(registerScreenServiceWorker({ serviceWorker: sw, secure: true })).resolves.toBeUndefined()
  })
})
