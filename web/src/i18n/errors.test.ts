import { describe, expect, it } from 'vitest'
import { createI18n } from './index'
import { translateError, translateErrorValue } from './errors'

describe('错误码翻译', () => {
  it('已知错误码按语言翻译', async () => {
    const zh = await createI18n('zh')
    const en = await createI18n('en')
    expect(translateError(zh, 'auth.invalid_password')).toBe('密码错误')
    expect(translateError(en, 'auth.invalid_password')).toBe('Wrong password')
    expect(translateError(zh, 'run.busy')).toContain('正在运行')
    expect(translateError(zh, 'ws.subscribe_denied')).not.toContain('操作失败')
    expect(translateError(zh, 'server.shutting_down')).not.toContain('操作失败')
  })

  it('未知错误码显示「操作失败（code）」', async () => {
    const zh = await createI18n('zh')
    const en = await createI18n('en')
    expect(translateError(zh, 'x.never_seen')).toBe('操作失败（x.never_seen）')
    expect(translateError(en, 'x.never_seen')).toBe('Operation failed (x.never_seen)')
  })

  it('不是错误对象时按未知处理', async () => {
    const zh = await createI18n('zh')
    expect(translateErrorValue(zh, new Error('boom'))).toBe('操作失败（unknown）')
    expect(translateErrorValue(zh, { code: 'proxy.in_use', details: { instances: ['a'] } })).toContain('代理')
  })
})
