import type { Device, Proxy } from '@/types/generated'

// 与中枢 releaseTagPattern 一致：只有正式发布的版本号才有对应的 GitHub Release
const releaseTag = /^v\d+\.\d+\.\d+$/

export type UpgradeCommand =
  | { kind: 'command'; text: string; proxyHint: string | null }
  | { kind: 'noRelease' }

// 代理的访问地址：socks5 在代理端解析域名时写成 socks5h
export function proxyUrl(p: Proxy): string {
  const scheme = p.scheme === 'socks5' && p.remote_dns ? 'socks5h' : p.scheme
  return `${scheme}://${p.address}`
}

// 升级命令：拨入设备从中枢下载（Linux 系统模式带 sudo，macOS 用户模式不带）；
// 拨出设备没有到中枢的下载通道，必须带 --release 从 GitHub Releases 升级，中枢不是正式发布版本时无命令。
// 设备经代理被中枢拨出时，提示访问 GitHub 不通可追加 --proxy（作为提示而不是命令的一部分）。
export function upgradeCommand(d: Device, hubVersion: string | null | undefined, proxy: Proxy | null): UpgradeCommand {
  const sudo = d.os === 'darwin' ? '' : 'sudo '
  if (d.direction !== 'dial_out') return { kind: 'command', text: `${sudo}pimon-agent upgrade`, proxyHint: null }
  if (!hubVersion || !releaseTag.test(hubVersion)) return { kind: 'noRelease' }
  return {
    kind: 'command',
    text: `${sudo}pimon-agent upgrade --release ${hubVersion}`,
    proxyHint: proxy ? proxyUrl(proxy) : null,
  }
}
