// 时长解析：与后端 time.ParseDuration 接受的写法一致（如 10s、1m30s、1.5h、300ms）。
// 无法解析或不为正数时返回 null。

const unitSeconds: Record<string, number> = {
  ns: 1e-9,
  us: 1e-6,
  µs: 1e-6,
  μs: 1e-6,
  ms: 1e-3,
  s: 1,
  m: 60,
  h: 3600,
}

export function parseDurationSeconds(raw: string): number | null {
  const s = raw.trim()
  if (s === '' || s === '0') return null
  let rest = s.startsWith('+') ? s.slice(1) : s
  if (rest === '' || rest.startsWith('-')) return null
  let total = 0
  while (rest !== '') {
    const m = /^(\d*\.?\d*)([a-zA-Zµμ]+)/.exec(rest)
    if (!m || m[1] === '' || m[1] === '.') return null
    const unit = unitSeconds[m[2]]
    if (unit === undefined) return null
    total += Number(m[1]) * unit
    rest = rest.slice(m[0].length)
  }
  return total > 0 ? total : null
}

// 把整数秒格式化为紧凑写法：90 -> 1m30s，3600 -> 1h，45 -> 45s
export function formatDurationSeconds(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return ''
  let rest = Math.round(sec)
  const h = Math.floor(rest / 3600)
  rest -= h * 3600
  const m = Math.floor(rest / 60)
  const s = rest - m * 60
  return `${h ? `${h}h` : ''}${m ? `${m}m` : ''}${s ? `${s}s` : ''}`
}
