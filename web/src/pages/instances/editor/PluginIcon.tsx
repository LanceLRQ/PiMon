import { createElement } from 'react'
import type { PluginInfo } from '@/types/generated'
import { pluginIcon } from './catalog'

// 插件图标：内置插件按 id 对应图标，其余用通用图标
export function PluginIcon({ plugin, size }: { plugin: PluginInfo; size: number }) {
  return createElement(pluginIcon(plugin), { size, 'aria-hidden': true })
}
