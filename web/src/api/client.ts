import { ApiError, isSessionExpired, networkErrorCode, timeoutErrorCode } from './errors'

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: unknown
  query?: Record<string, string | number | boolean | undefined>
  signal?: AbortSignal
  // 超时毫秒数；不传表示不设客户端超时（例如保存并测试按插件 timeout 另行计算）
  timeoutMs?: number
}

type UnauthorizedHandler = () => void

let onUnauthorized: UnauthorizedHandler | null = null

// 会话失效（401）时调用，由应用外壳注册：清掉本地会话状态并跳转登录页
export function setUnauthorizedHandler(handler: UnauthorizedHandler | null) {
  onUnauthorized = handler
}

function buildUrl(path: string, query: RequestOptions['query']): string {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined) params.set(k, String(v))
  }
  const qs = params.toString()
  return qs ? `${path}?${qs}` : path
}

async function parseError(res: Response): Promise<ApiError> {
  try {
    const body = (await res.json()) as { error?: { code?: string; details?: Record<string, unknown> } }
    if (body?.error?.code) return new ApiError(res.status, body.error.code, body.error.details ?? {})
  } catch {
    // 响应体不是约定的错误格式
  }
  return new ApiError(res.status, `http.${res.status}`)
}

// 浏览器会为同源的非 GET 请求自动附带 Origin，服务端据此校验；这里只需同源携带 Cookie。
export async function request<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
  const controller = new AbortController()
  const timer = opts.timeoutMs ? setTimeout(() => controller.abort(), opts.timeoutMs) : undefined
  opts.signal?.addEventListener('abort', () => controller.abort())
  let res: Response
  try {
    res = await fetch(buildUrl(path, opts.query), {
      method: opts.method ?? 'GET',
      credentials: 'same-origin',
      headers: opts.body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      signal: controller.signal,
    })
  } catch (e) {
    const timedOut = controller.signal.aborted && !opts.signal?.aborted
    if (!timedOut && opts.signal?.aborted) throw e
    throw new ApiError(0, timedOut ? timeoutErrorCode : networkErrorCode)
  } finally {
    if (timer) clearTimeout(timer)
  }
  if (!res.ok) {
    const err = await parseError(res)
    if (isSessionExpired(err)) onUnauthorized?.()
    throw err
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

export const http = {
  get: <T>(path: string, opts?: Omit<RequestOptions, 'method' | 'body'>) => request<T>(path, { ...opts, method: 'GET' }),
  post: <T>(path: string, body?: unknown, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    request<T>(path, { ...opts, method: 'POST', body }),
  put: <T>(path: string, body?: unknown, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    request<T>(path, { ...opts, method: 'PUT', body }),
  patch: <T>(path: string, body?: unknown, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    request<T>(path, { ...opts, method: 'PATCH', body }),
  delete: <T>(path: string, opts?: Omit<RequestOptions, 'method' | 'body'>) =>
    request<T>(path, { ...opts, method: 'DELETE' }),
}
