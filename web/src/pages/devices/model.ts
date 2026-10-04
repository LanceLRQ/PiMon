import type { i18n as I18n } from 'i18next'
import type { ShapeState } from '@/ui/status-shape'
import type { Device } from '@/types/generated'

// 设备页公共工具：设备列表、实例运行位置、总览计数、编辑器预览共用，不各写一份。

// 设备 id 到显示名：hub 是中枢本机（名称由调用方按语言传入）；设备已不存在时返回 id 并标记失效。
// 设备列表还没加载（null）时无法判断是否失效，不标记。
export function deviceName(
  id: string,
  devices: readonly Device[] | null,
  hubLabel: string,
): { name: string; missing: boolean } {
  if (id === 'hub') return { name: hubLabel, missing: false }
  const d = devices?.find((x) => x.id === id)
  if (d) return { name: d.name, missing: false }
  return { name: id, missing: devices !== null }
}

export type PresenceTone = 'ok' | 'weak' | 'warn' | 'muted' | 'neutral'

export interface PresenceView {
  shape: ShapeState
  // i18n 键：devices.presence.*
  labelKey: string
  // ok 正常；weak 弱警示（连接中断）；warn 警示（离线）；muted 灰色（休眠）；neutral 中性（等待连接、未知）
  tone: PresenceTone
  // 拨出设备凭据失效或被吊销：重连循环已停止，需要重新配对
  needsRepair: boolean
}

const presenceTable: Record<string, Pick<PresenceView, 'shape' | 'tone'>> = {
  online: { shape: 'ok', tone: 'ok' },
  disconnected: { shape: 'stale', tone: 'weak' },
  offline: { shape: 'offline', tone: 'warn' },
  dormant: { shape: 'dormant', tone: 'muted' },
  pending: { shape: 'unconfigured', tone: 'neutral' },
}

export function presenceView(d: Device): PresenceView {
  const known = presenceTable[d.presence]
  return {
    shape: known?.shape ?? 'unknown',
    tone: known?.tone ?? 'neutral',
    labelKey: known ? `devices.presence.${d.presence}` : 'status.unknown',
    needsRepair: d.conn_error === 'bad_credential' || d.conn_error === 'revoked',
  }
}

export interface UpgradeNeed {
  // required：协议不兼容，必须升级才能接入；differs：仅版本字符串不同，较弱的提示
  level: 'required' | 'differs'
  // 要升级的一方：agent 或中枢
  target: 'agent' | 'hub'
}

// 两级判定：incompatible 非空为「需要升级」（version_too_new 要升级中枢，其余升级 agent）；
// 否则版本字符串不同且都非空为「版本不同」。开发构建（中枢版本 dev）总是判为不同。
export function needsUpgrade(d: Device, hubVersion: string | null | undefined): UpgradeNeed | null {
  if (d.incompatible) {
    return { level: 'required', target: d.incompatible.reason === 'version_too_new' ? 'hub' : 'agent' }
  }
  if (d.agent_version && hubVersion && d.agent_version !== hubVersion) {
    return { level: 'differs', target: 'agent' }
  }
  return null
}

// 不兼容原因的文案：优先按原因取，查不到退回通用文案。
export function incompatibleText(i18n: I18n, reason: string | null | undefined): string {
  const key = `errors.device.incompatible_reason.${reason ?? ''}`
  return reason && i18n.exists(key) ? i18n.t(key) : i18n.t('errors.device.incompatible')
}
