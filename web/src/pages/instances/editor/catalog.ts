import { Activity, Braces, Clock, Cloud, Cpu, FlaskConical, Globe, Network, Plug, Puzzle, Server, type LucideIcon } from 'lucide-react'
import type { PluginInfo } from '@/types/generated'

// 插件目录的分组与图标。manifest 没有分类字段，内置插件按 id 归类，
// 其余（exec 插件或未收录的）归入「其他」。
export type CategoryKey = 'system' | 'host' | 'service' | 'network' | 'weather' | 'other'

export const categoryOrder: CategoryKey[] = ['system', 'host', 'service', 'network', 'weather', 'other']

const categoryOf: Record<string, CategoryKey> = {
  'hub-self': 'system',
  core: 'system',
  demo: 'system',
  'host-metrics': 'host',
  'http-check': 'service',
  'http-json': 'service',
  'tcp-check': 'service',
  ping: 'network',
  'net-reach': 'network',
  weather: 'weather',
}

const iconOf: Record<string, LucideIcon> = {
  'hub-self': Cpu,
  core: Clock,
  demo: FlaskConical,
  'host-metrics': Server,
  'http-check': Globe,
  'http-json': Braces,
  'tcp-check': Plug,
  ping: Activity,
  'net-reach': Network,
  weather: Cloud,
}

export function pluginCategory(p: PluginInfo): CategoryKey {
  return categoryOf[p.id] ?? 'other'
}

export function pluginIcon(p: PluginInfo): LucideIcon {
  return iconOf[p.id] ?? Puzzle
}

// 新建实例目录里隐藏的插件：core 由种子数据创建，用户不必也不应手动新建。
export const hiddenFromCatalog: ReadonlySet<string> = new Set(['core'])

// 只有数据源插件能创建监控实例，隐藏的插件除外
export function creatablePlugins(list: PluginInfo[]): PluginInfo[] {
  return list.filter((p) => p.kind === 'source' && !hiddenFromCatalog.has(p.id))
}
