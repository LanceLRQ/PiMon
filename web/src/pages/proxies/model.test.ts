import { describe, expect, it } from 'vitest'
import type { Proxy } from '@/types/generated'
import { buildInput, emptyForm, formDirty, formFromProxy, joinAddress, portValid, splitAddress } from './model'

const proxy: Proxy = {
  id: 'p1',
  name: 'HK-01',
  scheme: 'socks5h',
  address: '10.0.0.2:1080',
  remote_dns: true,
  location: 'any',
  auth: { set: true },
  referrers: [],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

describe('代理表单模型', () => {
  it('地址拆分与拼接，IPv6 自动补方括号', () => {
    expect(splitAddress('10.0.0.2:1080')).toEqual({ host: '10.0.0.2', port: '1080' })
    expect(splitAddress('[::1]:1080')).toEqual({ host: '[::1]', port: '1080' })
    expect(splitAddress('nohost')).toEqual({ host: 'nohost', port: '' })
    expect(joinAddress(' 10.0.0.2 ', ' 1080 ')).toBe('10.0.0.2:1080')
    expect(joinAddress('::1', '1080')).toBe('[::1]:1080')
    expect(joinAddress('[::1]', '1080')).toBe('[::1]:1080')
  })

  it('端口校验', () => {
    expect(portValid('1')).toBe(true)
    expect(portValid('65535')).toBe(true)
    expect(portValid('0')).toBe(false)
    expect(portValid('65536')).toBe(false)
    expect(portValid('10a')).toBe(false)
    expect(portValid('')).toBe(false)
  })

  it('编辑已有代理：认证留空时请求体不带 auth（保留原值）', () => {
    const form = formFromProxy(proxy)
    expect(form.username).toBe('')
    expect(form.password).toBe('')
    const body = buildInput(form)
    expect(body).toEqual({ name: 'HK-01', scheme: 'socks5h', address: '10.0.0.2:1080', remote_dns: true, location: 'any' })
    expect('auth' in body).toBe(false)
  })

  it('填了用户名或密码才提交 auth；清除认证时不带 auth', () => {
    const f = { ...formFromProxy(proxy), username: 'pimon', password: 'pw' }
    expect(buildInput(f).auth).toEqual({ username: 'pimon', password: 'pw' })
    const c = { ...f, clearAuth: true }
    const body = buildInput(c)
    expect(body.clear_auth).toBe(true)
    expect('auth' in body).toBe(false)
  })

  it('远端 DNS：socks5h 恒为真，http 与 https 不提交为真，socks5 取开关', () => {
    const base = { ...emptyForm(), host: 'h', port: '1' }
    expect(buildInput({ ...base, scheme: 'socks5h', remoteDns: false }).remote_dns).toBe(true)
    expect(buildInput({ ...base, scheme: 'http', remoteDns: true }).remote_dns).toBe(false)
    expect(buildInput({ ...base, scheme: 'socks5', remoteDns: true }).remote_dns).toBe(true)
    expect(buildInput({ ...base, scheme: 'socks5', remoteDns: false }).remote_dns).toBe(false)
  })

  it('改动检测', () => {
    const f = formFromProxy(proxy)
    expect(formDirty(f, formFromProxy(proxy))).toBe(false)
    expect(formDirty({ ...f, name: 'x' }, f)).toBe(true)
  })
})
