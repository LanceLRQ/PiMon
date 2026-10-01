import {
  Bell,
  Cpu,
  Ellipsis,
  Globe,
  HardDrive,
  LayoutDashboard,
  Monitor,
  Send,
  Server,
  Settings2,
  type LucideIcon,
} from 'lucide-react'

export interface NavSubItem {
  // 二级编号 a、b、c…
  key: string
  labelKey: string
  to: string
  // 是否算作该二级项的当前页（默认路径完全相等）
  match?: (pathname: string) => boolean
}

export interface NavItem {
  no: string
  icon: LucideIcon
  labelKey: string
  to: string
  // 一级项的当前页判定（默认：路径等于 to 或以 to/ 开头；总览只匹配根路径）
  match?: (pathname: string) => boolean
  sub?: NavSubItem[]
  // 后续里程碑的入口：灰显，不可点击
  milestone?: 'm2' | 'm4'
}

const under = (base: string) => (p: string) => p === base || p.startsWith(`${base}/`)

// 屏幕分组下的页面（屏幕管理、布局编辑器、时段计划、远程操作）在 M1d 实现，本期为占位页
export const navItems: NavItem[] = [
  { no: '01', icon: LayoutDashboard, labelKey: 'nav.overview', to: '/', match: (p) => p === '/' },
  {
    no: '02',
    icon: Monitor,
    labelKey: 'nav.screens',
    to: '/screens',
    sub: [
      { key: 'a', labelKey: 'nav.sub.screenManage', to: '/screens' },
      { key: 'b', labelKey: 'nav.sub.layoutEditor', to: '/screens/editor' },
      { key: 'c', labelKey: 'nav.sub.schedule', to: '/screens/schedule' },
      { key: 'd', labelKey: 'nav.sub.remote', to: '/screens/remote' },
    ],
  },
  {
    no: '03',
    icon: Server,
    labelKey: 'nav.instances',
    to: '/instances',
    sub: [
      {
        key: 'a',
        labelKey: 'nav.sub.instanceList',
        to: '/instances',
        match: (p) => p === '/instances' || (p.startsWith('/instances/') && p !== '/instances/new'),
      },
      { key: 'b', labelKey: 'nav.sub.instanceNew', to: '/instances/new' },
    ],
  },
  { no: '04', icon: Globe, labelKey: 'nav.proxies', to: '/proxies' },
  { no: '05', icon: Settings2, labelKey: 'nav.settings', to: '/settings' },
  { no: '06', icon: Cpu, labelKey: 'nav.system', to: '/system' },
]

// 后续阶段入口（侧栏分组「later」下灰显）
export const laterItems: NavItem[] = [
  { no: '07', icon: HardDrive, labelKey: 'nav.devices', to: '/devices', milestone: 'm2' },
  { no: '08', icon: Bell, labelKey: 'nav.alerts', to: '/alerts', milestone: 'm4' },
  { no: '09', icon: Send, labelKey: 'nav.channels', to: '/channels', milestone: 'm4' },
]

export function isItemActive(item: NavItem, pathname: string): boolean {
  return (item.match ?? under(item.to))(pathname)
}

export function isSubActive(sub: NavSubItem, pathname: string): boolean {
  return (sub.match ?? ((p: string) => p === sub.to))(pathname)
}

export interface MobileTab {
  key: string
  icon: LucideIcon
  labelKey: string
  to: string | null
}

// 手机底部标签栏：总览、屏幕、实例、设置、更多（更多是菜单，没有路径）
export const mobileTabs: MobileTab[] = [
  { key: '01', icon: LayoutDashboard, labelKey: 'nav.overview', to: '/' },
  { key: '02', icon: Monitor, labelKey: 'nav.screens', to: '/screens' },
  { key: '03', icon: Server, labelKey: 'nav.instancesShort', to: '/instances' },
  { key: '05', icon: Settings2, labelKey: 'nav.settings', to: '/settings' },
  { key: 'more', icon: Ellipsis, labelKey: 'nav.more', to: null },
]

// 当前路径属于哪个手机标签；不在前四个里就归到「更多」
export function activeMobileTab(pathname: string): string {
  const hit = navItems.find((it) => isItemActive(it, pathname))
  return hit && mobileTabs.some((t) => t.key === hit.no) ? hit.no : 'more'
}
