import type { i18n as I18n } from 'i18next'

// i18next 的保留选项名：details 里若出现这些键，不能原样作为插值参数传入，否则会改变翻译行为
const reservedOptions = new Set([
  'lng', 'lngs', 'fallbackLng', 'ns', 'context', 'count', 'ordinal', 'defaultValue', 'replace',
  'returnObjects', 'returnDetails', 'joinArrays', 'postProcess', 'interpolation', 'keySeparator', 'nsSeparator',
])

function safeParams(details?: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(details ?? {}).filter(([k]) => !reservedOptions.has(k)))
}

// 把错误码翻译为当前语言文案：错误码表以 src/internal/hub/httpx/errors.go 为准；
// 未收录的 code 显示「操作失败（code）」。details 作为插值参数传入。
export function translateError(i18n: I18n, code: string, details?: Record<string, unknown>): string {
  const key = `errors.${code}`
  if (code && i18n.exists(key)) return i18n.t(key, safeParams(details))
  return i18n.t('errorFallback', { code: code || 'unknown' })
}

// 对任意抛出的值取错误码：ApiError 带 code，其余按未知处理
export function errorCodeOf(err: unknown): { code: string; details?: Record<string, unknown> } {
  if (typeof err === 'object' && err !== null && 'code' in err && typeof (err as { code: unknown }).code === 'string') {
    const e = err as { code: string; details?: Record<string, unknown> }
    return { code: e.code, details: e.details }
  }
  return { code: 'unknown' }
}

export function translateErrorValue(i18n: I18n, err: unknown): string {
  const { code, details } = errorCodeOf(err)
  return translateError(i18n, code, details)
}
