// 时区下拉的选项：浏览器支持的 IANA 时区；取不到时退化为浏览器时区与 UTC
export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

function supportedTimezones(): string[] {
  try {
    const supported = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf
    return supported ? supported('timeZone') : []
  } catch {
    return []
  }
}

export function timezoneOptions(current: string): string[] {
  const list = supportedTimezones()
  const set = new Set(list)
  set.add('UTC')
  set.add(current)
  return Array.from(set).sort((a, b) => a.localeCompare(b))
}
