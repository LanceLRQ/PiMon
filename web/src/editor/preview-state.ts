import type { InstanceDataMap } from '@/templates'
import type { ResolvedScreen } from '@/types/generated'

// 预览状态：只改画布收到的副本，让所有小组件按指定级别着色，用于查看各状态的外观。
// 不进编辑器状态、不写草稿、不参与保存；借用模板现有的通道：
// 展示状态（标题栏标记）、state 数据项的 state、options.threshold（数值项与外框着色）。

export type PreviewState = 'real' | 'ok' | 'warning' | 'critical'

export const previewStates: readonly PreviewState[] = ['real', 'ok', 'warning', 'critical']

// 比任何真实读数都小或大的界限：让数值项无论取值多少都落在指定级别
const LOW = -1e15
const HIGH = 1e15

function forcedThreshold(level: Exclude<PreviewState, 'real'>) {
  const warning = level === 'ok' ? HIGH : LOW
  const critical = level === 'critical' ? LOW : HIGH
  return { enabled: true, direction: 'above', warning, critical }
}

/** 未配置与引用失效的小组件没有数据可着色，保持占位 */
const keepAsIs = new Set(['unconfigured', 'broken'])

export function applyPreviewState(
  screen: ResolvedScreen,
  data: InstanceDataMap,
  preview: PreviewState,
): { screen: ResolvedScreen; data: InstanceDataMap } {
  if (preview === 'real') return { screen, data }
  const threshold = forcedThreshold(preview)
  const widgets = screen.widgets.map((w) =>
    keepAsIs.has(w.display_state) ? w : { ...w, display_state: preview, options: { ...w.options, threshold } },
  )
  const nextData: Record<string, NonNullable<InstanceDataMap[string]>> = {}
  for (const [id, d] of Object.entries(data)) {
    if (!d) continue
    nextData[id] = {
      ...d,
      display_state: preview,
      report_status: preview,
      report_stale: false,
      items: d.items.map((it) => (it.type === 'state' ? { ...it, state: preview } : it)),
    }
  }
  return { screen: { ...screen, widgets }, data: nextData }
}
