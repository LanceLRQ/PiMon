import {
  DEFAULT_THEME_ID,
  getTheme,
} from './registry'
import {
  statusLevels,
  statusMarkers,
  statusWeights,
  type StatusLevel,
  type StatusMarker,
  type StatusWeight,
  type ThemeDensity,
  type ThemeId,
} from './types'

export interface StatusChannel {
  marker: StatusMarker
  weight: StatusWeight
}

/** 主题切换时由计算样式读出的关键字型 token，供 SVG 与模板逻辑使用 */
export interface ThemeRuntime {
  themeId: ThemeId
  density: ThemeDensity
  reduceEffects: boolean
  status: Record<StatusLevel, StatusChannel>
}

// 读不到时的兜底：五级 marker 互异，critical 带强调
const fallbackStatus: Record<StatusLevel, StatusChannel> = {
  ok: { marker: 'dot', weight: 'normal' },
  warning: { marker: 'ring', weight: 'normal' },
  critical: { marker: 'triangle', weight: 'strong' },
  unknown: { marker: 'diamond', weight: 'normal' },
  error: { marker: 'square', weight: 'normal' },
}

const densities: readonly ThemeDensity[] = ['airy', 'regular', 'compact']

function pick<T extends string>(raw: string, allowed: readonly T[], fallback: T): T {
  const v = raw.trim() as T
  return allowed.includes(v) ? v : fallback
}

/** 从元素（通常是屏幕根或画布容器）读出当前主题的运行时数据 */
export function readThemeRuntime(el: Element = document.documentElement): ThemeRuntime {
  const style = getComputedStyle(el)
  const get = (name: string) => style.getPropertyValue(name)
  const status = {} as Record<StatusLevel, StatusChannel>
  for (const level of statusLevels) {
    status[level] = {
      marker: pick(get(`--status-${level}-marker`), statusMarkers, fallbackStatus[level].marker),
      weight: pick(get(`--status-${level}-weight`), statusWeights, fallbackStatus[level].weight),
    }
  }
  const themeHost = el.closest('[data-theme]')
  return {
    themeId: getTheme(themeHost?.getAttribute('data-theme') ?? DEFAULT_THEME_ID).id,
    density: pick(get('--density'), densities, 'regular'),
    reduceEffects: el.closest('[data-reduce-effects]') !== null,
    status,
  }
}

/**
 * 监听主题或降低特效属性的变化（属性可在元素自身或祖先上变化）。
 * 返回取消函数。回调在属性变更后的微任务里触发，此时样式已按新主题重算。
 */
export function watchThemeRuntime(el: Element, onChange: (rt: ThemeRuntime) => void): () => void {
  const root = el.ownerDocument.documentElement
  const observer = new MutationObserver(() => onChange(readThemeRuntime(el)))
  const options = { attributes: true, attributeFilter: ['data-theme', 'data-reduce-effects'] }
  observer.observe(el, options)
  for (let n = el.parentElement; n; n = n.parentElement) observer.observe(n, options)
  if (!root.contains(el)) observer.observe(root, options)
  return () => observer.disconnect()
}
