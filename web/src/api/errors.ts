// 统一的 API 错误：code 取自响应体 {"error":{"code","details"}}，由前端按 code 翻译
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: Record<string, unknown>

  constructor(status: number, code: string, details: Record<string, unknown> = {}) {
    super(code)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
  }
}

export const networkErrorCode = 'network.failed'
export const timeoutErrorCode = 'request.timeout'

// 登录/设置码校验失败也是 401，但不代表会话失效，不能触发跳转登录页
const credentialCodes = new Set(['auth.invalid_password', 'setup.invalid_code', 'auth.locked'])

export function isSessionExpired(err: ApiError): boolean {
  return err.status === 401 && !credentialCodes.has(err.code)
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError
}
