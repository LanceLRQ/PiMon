import {
  Activity, Bell, Bot, Clock, Cloud, Container, Cpu, Database, Gauge, Gem, Globe, HardDrive, House,
  MemoryStick, Network, Server, Shield, Thermometer, Timer, Wallet, Wifi, Zap,
  type LucideIcon,
} from 'lucide-react'

// options.icon 可选的 lucide 图标：按需列出，不整包引入 lucide。编辑器的图标选择器共用这份清单。
export const widgetIcons: Readonly<Record<string, LucideIcon>> = {
  activity: Activity,
  bell: Bell,
  bot: Bot,
  clock: Clock,
  cloud: Cloud,
  container: Container,
  cpu: Cpu,
  database: Database,
  gauge: Gauge,
  gem: Gem,
  globe: Globe,
  'hard-drive': HardDrive,
  house: House,
  'memory-stick': MemoryStick,
  network: Network,
  server: Server,
  shield: Shield,
  thermometer: Thermometer,
  timer: Timer,
  wallet: Wallet,
  wifi: Wifi,
  zap: Zap,
}

export const widgetIconNames: readonly string[] = Object.keys(widgetIcons)

export function hasWidgetIcon(name: unknown): name is string {
  return typeof name === 'string' && Object.hasOwn(widgetIcons, name)
}

/** 按 options.icon 画图标；名字不在清单里不画 */
export function WidgetIcon({ name, size = 14, className }: { name: unknown; size?: number; className?: string }) {
  if (!hasWidgetIcon(name)) return null
  const Icon = widgetIcons[name]
  return <Icon size={size} className={className} aria-hidden="true" />
}
