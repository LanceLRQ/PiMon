// url 字段的即时校验：规则与后端 schema.CheckURL 一致（仅 http/https、必须有主机、
// 禁止内嵌凭据；查询参数与公网 http 需字段显式允许）。最终以服务端校验为准。

const ipv4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/

function isPrivateV4(h: string): boolean | null {
  const m = ipv4.exec(h)
  if (!m) return null
  const [a, b] = [Number(m[1]), Number(m[2])]
  return (
    a === 10 ||
    a === 127 ||
    a === 0 ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) ||
    (a === 169 && b === 254) ||
    (a === 100 && b >= 64 && b <= 127)
  )
}

function isPublicHost(hostname: string): boolean {
  let h = hostname.toLowerCase()
  if (h.startsWith('[') && h.endsWith(']')) {
    const v6 = h.slice(1, -1)
    return !(v6 === '::1' || v6 === '::' || /^f[cd]/.test(v6) || /^fe[89ab]/.test(v6))
  }
  const priv = isPrivateV4(h)
  if (priv !== null) return !priv
  if (h.endsWith('.')) h = h.slice(0, -1)
  if (h === 'localhost' || !h.includes('.')) return false
  return !['.localhost', '.local', '.lan', '.internal', '.home.arpa', '.localdomain'].some((suf) => h.endsWith(suf))
}

// 返回 true 表示地址合法
export function checkUrl(raw: string, allowQuery: boolean, allowPublicHttp: boolean): boolean {
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return false
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return false
  if (u.hostname === '') return false
  if (u.username !== '' || u.password !== '') return false
  if (!allowQuery && (u.search !== '' || /\?(#|$)/.test(raw))) return false
  if (u.protocol === 'http:' && !allowPublicHttp && isPublicHost(u.hostname)) return false
  return true
}
