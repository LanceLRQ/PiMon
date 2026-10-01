// 屏幕端离线外壳：手写 Service Worker，作用域 /screen（由 hub 以 Service-Worker-Allowed: / 放行）。
// - /screen 下的页面导航：network-first，网络失败或 5xx 时回落到缓存的外壳，连外壳都没有就返回自带的「hub 未运行」页。
// - /assets/*：cache-first（文件名带哈希，内容不可变）。
// - 不维护预缓存清单，不碰 WebSocket；最后一份 snapshot 由页面自己存（见 src/screen/snapshot-cache.ts）。
// 与页面里 src/screen/offline.ts 的缓存名必须一致。

const SHELL_CACHE = 'pimon-screen-shell-v1'
// 所有 /screen 下的导航共用同一份外壳（index.html 与路径无关）
const SHELL_KEY = '/screen'
// /assets 缓存条目上限：文件名带哈希，升级后旧文件不会再被引用，超出按写入顺序淘汰最旧的。
// 一个 build 约 20 个文件，80 条够留下三四个版本。
const MAX_ASSETS = 80

const OFFLINE_PAGE = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>PiMon</title>
<style>
html,body{margin:0;height:100%;background:#0b0f14;color:#d6dde6;font-family:system-ui,sans-serif}
body{display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center;gap:.6rem}
h1{font-size:2rem;margin:0}p{margin:0;opacity:.7;font-size:1.1rem}
</style>
</head>
<body>
<h1>hub 未运行</h1>
<p>请确认树莓派上的 pimon-hub 正在运行，恢复后本页会自动重试。</p>
<p>The hub is not running. This page retries automatically.</p>
<script>setTimeout(function(){location.reload()},10000)</script>
</body>
</html>`

function offlineResponse() {
  return new Response(OFFLINE_PAGE, {
    status: 503,
    headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' },
  })
}

function isScreenNavigation(request, url) {
  if (request.mode !== 'navigate') return false
  const p = url.pathname
  if (p !== '/screen' && !p.startsWith('/screen/')) return false
  // 令牌换会话的跳转必须直通服务端
  return p !== '/screen/auth' && !p.startsWith('/screen/auth/')
}

async function navigate(request, deps) {
  const cache = await deps.caches.open(SHELL_CACHE)
  try {
    const res = await deps.fetch(request)
    if (res.ok) {
      await cache.put(SHELL_KEY, res.clone())
      return res
    }
    // 只有服务端故障（5xx，如反代 502、hub 正在重启）才回落外壳；302、401、404 等是真实的应答，不能被旧外壳掩盖
    if (res.status < 500) return res
    const cached = await cache.match(SHELL_KEY)
    return cached || res
  } catch {
    const cached = await cache.match(SHELL_KEY)
    return cached || offlineResponse()
  }
}

async function asset(request, deps) {
  const cache = await deps.caches.open(SHELL_CACHE)
  const hit = await cache.match(request)
  if (hit) return hit
  const res = await deps.fetch(request)
  if (res.ok) {
    await cache.put(request, res.clone())
    await trimAssets(cache)
  }
  return res
}

async function trimAssets(cache) {
  const keys = await cache.keys()
  const assets = keys.filter((k) => new URL(k.url).pathname.startsWith('/assets/'))
  for (const k of assets.slice(0, Math.max(0, assets.length - MAX_ASSETS))) await cache.delete(k)
}

// 返回 null 表示不处理（交给浏览器走网络）；否则返回响应的 Promise
function handleFetch(request, deps) {
  if (request.method !== 'GET') return null
  const url = new URL(request.url)
  if (url.origin !== deps.origin) return null
  if (isScreenNavigation(request, url)) return navigate(request, deps)
  if (url.pathname.startsWith('/assets/')) return asset(request, deps)
  return null
}

if (typeof self !== 'undefined' && typeof self.addEventListener === 'function') {
  const deps = () => ({
    fetch: (r) => fetch(r),
    caches,
    origin: self.location.origin,
  })

  self.addEventListener('install', (event) => {
    // 先把外壳放进缓存，这样首次访问后马上断网也能恢复；失败（hub 正好不可达）不影响安装
    event.waitUntil(
      caches
        .open(SHELL_CACHE)
        .then((c) => c.add(SHELL_KEY))
        .catch(() => {})
        .then(() => self.skipWaiting()),
    )
  })

  self.addEventListener('activate', (event) => {
    event.waitUntil(
      caches
        .keys()
        .then((names) => Promise.all(names.filter((n) => n.startsWith('pimon-screen-shell-') && n !== SHELL_CACHE).map((n) => caches.delete(n))))
        .then(() => self.clients.claim()),
    )
  })

  self.addEventListener('fetch', (event) => {
    const p = handleFetch(event.request, deps())
    if (p) event.respondWith(p)
  })
}
