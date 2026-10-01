export const setupCodeGroups = 6
export const setupCodeGroupLen = 4
export const setupCodeLength = setupCodeGroups * setupCodeGroupLen

// 只保留字母数字并转大写：设置码不区分大小写，分隔符与空白一律忽略
export function normalizeSetupCode(raw: string): string {
  return raw.replace(/[^0-9a-z]/gi, '').toUpperCase()
}

// 把整串设置码拆成 6 组 × 4 位；不足 24 位时后面的组为空串，超出部分丢弃
export function splitSetupCode(raw: string): string[] {
  const v = normalizeSetupCode(raw).slice(0, setupCodeLength)
  return Array.from({ length: setupCodeGroups }, (_, i) => v.slice(i * setupCodeGroupLen, (i + 1) * setupCodeGroupLen))
}

export function joinSetupCode(groups: string[]): string {
  return groups.join('')
}

export function isSetupCodeComplete(groups: string[]): boolean {
  return groups.every((g) => g.length === setupCodeGroupLen)
}
