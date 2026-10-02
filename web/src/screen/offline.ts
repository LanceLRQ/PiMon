import { shellCacheName } from './shell-cache'

// 注册屏幕端 Service Worker（public/screen/sw.js）并把已加载的 /assets/* 补进外壳缓存。
// Service Worker 只在安全上下文可用（kiosk 走 127.0.0.1 属于安全上下文）；其它情况静默跳过。
// 补缓存的原因：首次加载时脚本与样式在 Service Worker 接管页面之前就请求完了，没有经过它的 cache-first，
// 不补的话「首次访问后立刻断 hub 再刷新」会因缺少脚本而白屏。

export const serviceWorkerUrl = '/screen/sw.js'
export const serviceWorkerScope = '/screen'

export interface OfflineDeps {
  serviceWorker?: Pick<ServiceWorkerContainer, 'register' | 'ready'>
  secure?: boolean
  /** 页面已加载的资源地址 */
  resourceUrls?: () => string[]
  caches?: Pick<CacheStorage, 'open'>
  fetch?: typeof fetch
  origin?: string
}

function defaultResourceUrls(): string[] {
  return performance.getEntriesByType('resource').map((e) => e.name)
}

export async function registerScreenServiceWorker(deps: OfflineDeps = {}): Promise<void> {
  const container = deps.serviceWorker ?? (typeof navigator !== 'undefined' ? navigator.serviceWorker : undefined)
  const secure = deps.secure ?? (typeof window !== 'undefined' && window.isSecureContext)
  if (!container || !secure) return
  try {
    await container.register(serviceWorkerUrl, { scope: serviceWorkerScope })
    await container.ready
    await warmAssetCache(deps)
  } catch {
    // 注册失败（浏览器策略、hub 暂时不可达）只是失去离线外壳，不影响在线使用
  }
}

async function warmAssetCache(deps: OfflineDeps) {
  const storage = deps.caches ?? (typeof caches !== 'undefined' ? caches : undefined)
  if (!storage) return
  const origin = deps.origin ?? location.origin
  const doFetch = deps.fetch ?? fetch
  const urls = (deps.resourceUrls ?? defaultResourceUrls)().filter((u) => {
    try {
      const parsed = new URL(u)
      return parsed.origin === origin && parsed.pathname.startsWith('/assets/')
    } catch {
      return false
    }
  })
  if (urls.length === 0) return
  const cache = await storage.open(shellCacheName)
  await Promise.all(
    urls.map(async (u) => {
      if (await cache.match(u)) return
      const res = await doFetch(u)
      if (res.ok) await cache.put(u, res)
    }),
  )
}
