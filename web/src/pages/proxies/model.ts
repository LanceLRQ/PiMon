import type { Proxy, ProxyInput } from '@/types/generated'

export const schemes = ['http', 'https', 'socks5', 'socks5h'] as const
export type Scheme = (typeof schemes)[number]
export const locations = ['hub', 'lan', 'any'] as const
export type Location = (typeof locations)[number]

export const defaultTestUrl = 'https://www.google.com/generate_204'

// 代理编辑表单的状态。认证只写不读：用户名与密码总是从空开始，留空表示保留原值。
export interface ProxyForm {
  name: string
  scheme: Scheme
  host: string
  port: string
  username: string
  password: string
  clearAuth: boolean
  remoteDns: boolean
  location: Location
}

export function emptyForm(): ProxyForm {
  return { name: '', scheme: 'socks5h', host: '', port: '', username: '', password: '', clearAuth: false, remoteDns: true, location: 'any' }
}

// 「host:port」按最后一个冒号拆开，IPv6 的方括号保留在 host 里
export function splitAddress(address: string): { host: string; port: string } {
  const i = address.lastIndexOf(':')
  if (i < 0) return { host: address, port: '' }
  return { host: address.slice(0, i), port: address.slice(i + 1) }
}

// 裸的 IPv6 地址（含冒号且没有方括号）自动补方括号
export function joinAddress(host: string, port: string): string {
  const h = host.trim()
  const wrapped = h.includes(':') && !h.startsWith('[') ? `[${h}]` : h
  return `${wrapped}:${port.trim()}`
}

export function formFromProxy(p: Proxy): ProxyForm {
  const { host, port } = splitAddress(p.address)
  const scheme = (schemes as readonly string[]).includes(p.scheme) ? (p.scheme as Scheme) : 'socks5h'
  const location = (locations as readonly string[]).includes(p.location) ? (p.location as Location) : 'any'
  return { name: p.name, scheme, host, port, username: '', password: '', clearAuth: false, remoteDns: p.remote_dns, location }
}

// http 与 https 代理天然由代理端解析域名，远端 DNS 对它们不适用
export function remoteDnsApplies(scheme: Scheme): boolean {
  return scheme === 'socks5' || scheme === 'socks5h'
}

export function portValid(port: string): boolean {
  if (!/^\d{1,5}$/.test(port.trim())) return false
  const n = Number(port)
  return n >= 1 && n <= 65535
}

// 表单转请求体：用户名与密码都为空时不带 auth（编辑时即保留原值）
export function buildInput(f: ProxyForm): ProxyInput {
  const input: ProxyInput = {
    name: f.name.trim(),
    scheme: f.scheme,
    address: joinAddress(f.host, f.port),
    remote_dns: f.scheme === 'socks5h' ? true : f.scheme === 'socks5' ? f.remoteDns : false,
    location: f.location,
  }
  if (f.clearAuth) input.clear_auth = true
  else if (f.username !== '' || f.password !== '') input.auth = { username: f.username, password: f.password }
  return input
}

// 表单是否相对基线有改动（决定「保存」按钮与「测试使用已保存配置」的提示）
export function formDirty(a: ProxyForm, b: ProxyForm): boolean {
  return (Object.keys(a) as (keyof ProxyForm)[]).some((k) => a[k] !== b[k])
}
