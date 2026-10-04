import type { AgentInfo, Device, PairingResult, Proxy } from '@/types/generated'

// 添加设备对话框的纯函数：命令改写、配对设备判定、倒计时与下载命令。

export const deviceRoles = ['server', 'nas', 'dev', 'vps'] as const
export type DeviceRole = (typeof deviceRoles)[number]

// 配对码有效期（与中枢 devices.PairingCodeTTL 一致），只用于推算签发时间
export const pairingCodeTtlMs = 15 * 60 * 1000
export const maxNameRunes = 64
// 拨出模式下 agent 的默认监听端口（pimon-agent install --listen 缺省值）
export const agentListenPort = 31418

// 角色为开发机时「允许离线」默认开
export function defaultAllowOffline(role: string): boolean {
  return role === 'dev'
}

// NAS 与服务器上才有硬盘 SMART 等存储健康数据
export function wantsSudoersHint(role: string): boolean {
  return role === 'nas' || role === 'server'
}

export function nameTooLong(name: string): boolean {
  return [...name.trim()].length > maxNameRunes
}

// 中枢把 agent 通道地址写成 --hub <urls[0]>；换地址只改这一处，下载地址（web）不动。
function withHub(text: string, from: string, to: string): string {
  return from === to ? text : text.split(`--hub ${from}`).join(`--hub ${to}`)
}

const sudoPrefix = '$([ "$(uname -s)" = Darwin ] || echo sudo) ./pimon-agent install'

export interface PairingCommands {
  oneLine: string
  steps: string[]
}

// 按所选 hub 地址与角色整理命令：开发机（macOS 用户模式）的最后一步不带 sudo。
export function pairingCommands(result: PairingResult, hub: string, role: string): PairingCommands {
  const from = result.urls[0] ?? hub
  const steps = result.steps.map((s, i) => {
    let step = i === result.steps.length - 1 ? withHub(s, from, hub) : s
    if (role === 'dev') step = step.replace(sudoPrefix, './pimon-agent install')
    return step
  })
  return { oneLine: withHub(result.command, from, hub), steps }
}

// 判定本次拨入配对对应的设备：打开前不存在、拨入、创建时间不早于配对码签发时间；
// 同时出现多个时优先名字以所填名称开头的。issuedAtMs 为服务端时间。
export function findPairedDevice(
  devices: readonly Device[] | null,
  baseline: ReadonlySet<string>,
  issuedAtMs: number,
  name: string,
): Device | null {
  const prefix = name.trim()
  const fresh = (devices ?? []).filter(
    (d) => !baseline.has(d.id) && d.direction === 'dial_in' && (Date.parse(d.created_at) || 0) >= issuedAtMs,
  )
  if (fresh.length <= 1) return fresh[0] ?? null
  return (prefix ? fresh.find((d) => d.name.startsWith(prefix)) : undefined) ?? fresh[0]
}

export function formatCountdown(ms: number): string {
  const sec = Math.max(0, Math.ceil(ms / 1000))
  return `${String(Math.floor(sec / 60)).padStart(2, '0')}:${String(sec % 60).padStart(2, '0')}`
}

// hub 能用的代理：位置为 hub、lan、any 都行（hub 只拨出，不受 VPS 限制）
export function hubUsableProxies(list: readonly Proxy[]): Proxy[] {
  return list.filter((p) => p.location === 'hub' || p.location === 'lan' || p.location === 'any')
}

// 拨出（VPS）从 GitHub Releases 下载并校验的命令；开发构建没有 Release（模板为空）时返回 null。
export function releaseDownloadCommand(info: AgentInfo, platform: string): string | null {
  if (!info.release_url_template) return null
  const url = info.release_url_template.replace('<os>-<arch>', '$PLATFORM')
  const sums = url.replace(/\/[^/]*$/, '/SHA256SUMS')
  const sum = info.release_checksums[platform]
  const check = sum
    ? `echo "${sum}  pimon-agent-$PLATFORM" | sha256sum -c -`
    : `curl -fsSL ${sums} | grep " pimon-agent-$PLATFORM$" | sha256sum -c -`
  return [`PLATFORM=${platform}`, `curl -fsSLo pimon-agent-$PLATFORM ${url}`, check].join('\n')
}

// 安装为监听模式并开启配对窗口。有下载命令时沿用其中的 pimon-agent-$PLATFORM 文件名；
// 开发构建没有 Release，二进制由用户自己放到 VPS，文件名按 pimon-agent 写。
export function listenInstallCommand(afterDownload: boolean): string {
  const bin = afterDownload ? 'pimon-agent-$PLATFORM' : 'pimon-agent'
  return [`chmod +x ${bin}`, `sudo ./${bin} install --listen :${agentListenPort}`, 'sudo pimon-agent pair'].join('\n')
}

// 失败原因对应的指引条数（文案键 devices.add.fail.<reason>.tip1..tipN）
export const failTipCounts: Record<string, number> = {
  unreachable: 4,
  pin_mismatch: 3,
  code_expired: 2,
  bad_code: 2,
  rate_limited: 2,
  incompatible: 2,
  timeout: 2,
  unknown: 2,
}
