import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { http, request, setUnauthorizedHandler } from './client'
import { ApiError } from './errors'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('REST 客户端', () => {
  const fetchMock = vi.fn()
  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
    setUnauthorizedHandler(null)
  })
  afterEach(() => vi.unstubAllGlobals())

  it('成功时解析 JSON，POST 带 JSON 请求体与同源凭据', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { ok: true }))
    const out = await http.post<{ ok: boolean }>('/api/login', { password: 'x' })
    expect(out.ok).toBe(true)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/login')
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('same-origin')
    expect(init.body).toBe('{"password":"x"}')
  })

  it('204 返回 undefined，查询参数被拼接', async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))
    expect(await http.get('/api/x', { query: { a: 1, b: undefined } })).toBeUndefined()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/x?a=1')
  })

  it('错误响应抛 ApiError，带 code 与 details', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { error: { code: 'proxy.in_use', details: { instances: ['a'] } } }))
    const err = (await request('/api/proxies/p').catch((e: unknown) => e)) as ApiError
    expect(err).toBeInstanceOf(ApiError)
    expect(err.code).toBe('proxy.in_use')
    expect(err.details.instances).toEqual(['a'])
    expect(err.status).toBe(409)
  })

  it('401 触发会话失效回调', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'auth.required', details: {} } }))
    await expect(request('/api/instances')).rejects.toMatchObject({ code: 'auth.required' })
    expect(handler).toHaveBeenCalledTimes(1)
  })

  it('登录密码错误的 401 不触发会话失效回调', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'auth.invalid_password', details: { remaining: 3 } } }))
    await expect(request('/api/login', { method: 'POST', body: {} })).rejects.toMatchObject({ code: 'auth.invalid_password' })
    expect(handler).not.toHaveBeenCalled()
  })

  it('网络失败映射为 network.failed，非约定错误体映射为 http.<状态码>', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('fetch failed'))
    await expect(request('/api/x')).rejects.toMatchObject({ code: 'network.failed', status: 0 })
    fetchMock.mockResolvedValueOnce(new Response('<html>', { status: 502 }))
    await expect(request('/api/x')).rejects.toMatchObject({ code: 'http.502', status: 502 })
  })

  it('超过 timeoutMs 映射为 request.timeout', async () => {
    vi.useFakeTimers()
    fetchMock.mockImplementation(
      (_url: string, init: RequestInit) =>
        new Promise((_, reject) => init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))),
    )
    const p = request('/api/slow', { timeoutMs: 1000 }).catch((e) => e)
    await vi.advanceTimersByTimeAsync(1001)
    expect(await p).toMatchObject({ code: 'request.timeout' })
    vi.useRealTimers()
  })

  it('调用方信号已中止时立即中止请求；结束后移除监听', async () => {
    const ac = new AbortController()
    ac.abort()
    fetchMock.mockImplementation(async (_u: string, init: RequestInit) => {
      expect(init.signal?.aborted).toBe(true)
      throw new DOMException('aborted', 'AbortError')
    })
    await expect(request('/api/x', { signal: ac.signal })).rejects.toBeTruthy()

    const live = new AbortController()
    const remove = vi.spyOn(live.signal, 'removeEventListener')
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await request('/api/x', { signal: live.signal })
    expect(remove).toHaveBeenCalledWith('abort', expect.any(Function))
  })
})
