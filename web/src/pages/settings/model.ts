import type { BackupRemote, BackupRemoteInput, Settings } from '@/types/generated'

// 设置页的脏状态检测与保存前规整。所有比较都基于规整后的值，首尾空白不算修改。
// screen（屏幕显示参数）暂无界面，保存时原样回传，不参与脏检测。

export type SettingsKey =
  | 'language'
  | 'timezone'
  | 'access_url'
  | 'https_enabled'
  | 'trusted_proxies'
  | 'reduce_effects'
  | 'retention.raw_hours'
  | 'retention.five_min_days'
  | 'retention.hour_days'
  | 'backup.daily_at'
  | 'backup.keep'

export function normalizeForSave(s: Settings): Settings {
  return {
    ...s,
    access_url: s.access_url.trim(),
    trusted_proxies: s.trusted_proxies.map((p) => p.trim()).filter((p) => p !== ''),
    screen: { ...s.screen },
    retention: { ...s.retention },
    backup: { ...s.backup },
  }
}

// 返回与基线不同的设置项（trusted_proxies 整个列表算一项）
export function dirtyKeys(draft: Settings, base: Settings): SettingsKey[] {
  const a = normalizeForSave(draft)
  const b = normalizeForSave(base)
  const out: SettingsKey[] = []
  if (a.language !== b.language) out.push('language')
  if (a.timezone !== b.timezone) out.push('timezone')
  if (a.access_url !== b.access_url) out.push('access_url')
  if (a.https_enabled !== b.https_enabled) out.push('https_enabled')
  if (a.trusted_proxies.length !== b.trusted_proxies.length || a.trusted_proxies.some((p, i) => p !== b.trusted_proxies[i])) {
    out.push('trusted_proxies')
  }
  if (a.reduce_effects !== b.reduce_effects) out.push('reduce_effects')
  if (a.retention.raw_hours !== b.retention.raw_hours) out.push('retention.raw_hours')
  if (a.retention.five_min_days !== b.retention.five_min_days) out.push('retention.five_min_days')
  if (a.retention.hour_days !== b.retention.hour_days) out.push('retention.hour_days')
  if (a.backup.daily_at !== b.backup.daily_at) out.push('backup.daily_at')
  if (a.backup.keep !== b.backup.keep) out.push('backup.keep')
  return out
}

// 服务端对反代列表的错误键形如 trusted_proxies[2]，取出各行的错误码
export function trustedProxyErrors(fields: Record<string, string>): Record<number, string> {
  const out: Record<number, string> = {}
  for (const [k, v] of Object.entries(fields)) {
    const m = /^trusted_proxies\[(\d+)\]$/.exec(k)
    if (m) out[Number(m[1])] = v
  }
  return out
}

// ---- NAS 备份（远端备份目标）----
// 字段错误键由服务端返回不带前缀的键，并入设置页的错误表时统一加这个前缀
export const remoteErrorPrefix = 'backup.remote.'

// 读取失败时的降级表单：与服务端默认值一致（SMB、445、保留 30 份），未启用
export function defaultRemoteForm(): BackupRemoteInput {
  return {
    enabled: false,
    type: 'smb',
    smb_host: '',
    smb_port: 445,
    smb_share: '',
    smb_dir: '',
    smb_username: '',
    smb_password: '',
    smb_domain: '',
    webdav_url: '',
    webdav_dir: '',
    webdav_username: '',
    webdav_password: '',
    passphrase: '',
    keep: 30,
  }
}

// 服务端配置转表单草稿：密钥字段只回显「是否已设置」，草稿里一律为空串（留空即保留原值）
export function remoteToForm(r: BackupRemote): BackupRemoteInput {
  return {
    enabled: r.enabled,
    type: r.type,
    smb_host: r.smb_host,
    smb_port: r.smb_port,
    smb_share: r.smb_share,
    smb_dir: r.smb_dir,
    smb_username: r.smb_username,
    smb_password: '',
    smb_domain: r.smb_domain,
    webdav_url: r.webdav_url,
    webdav_dir: r.webdav_dir,
    webdav_username: r.webdav_username,
    webdav_password: '',
    passphrase: '',
    keep: r.keep,
  }
}

// 提交与比较前的规整：非密钥文本去首尾空白，密钥原样（口令里的空格是有意义的）
export function normalizeRemoteForSave(f: BackupRemoteInput): BackupRemoteInput {
  return {
    ...f,
    smb_host: f.smb_host.trim(),
    smb_share: f.smb_share.trim(),
    smb_dir: f.smb_dir.trim(),
    smb_username: f.smb_username.trim(),
    smb_domain: f.smb_domain.trim(),
    webdav_url: f.webdav_url.trim(),
    webdav_dir: f.webdav_dir.trim(),
    webdav_username: f.webdav_username.trim(),
  }
}

// 与基线不同的字段名；密钥字段只要非空就算修改
export function remoteDirtyKeys(draft: BackupRemoteInput, base: BackupRemoteInput): (keyof BackupRemoteInput)[] {
  const a = normalizeRemoteForSave(draft)
  const b = normalizeRemoteForSave(base)
  return (Object.keys(a) as (keyof BackupRemoteInput)[]).filter((k) => a[k] !== b[k])
}
