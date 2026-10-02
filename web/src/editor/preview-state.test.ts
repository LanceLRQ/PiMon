import { describe, expect, it } from 'vitest'
import { parseThreshold, thresholdLevel } from '@/templates/data'
import type { ResolvedScreen, ResolvedWidget, ScreenInstanceData } from '@/types/generated'
import { applyPreviewState } from './preview-state'

const widget = (id: string, display_state = 'ok'): ResolvedWidget => ({
  id, source: 'generic', template: 'gauge', size: { cols: 1, rows: 1 }, col: 0, row: 0, title: id, slots: {}, display_state,
  options: { title: 'x', threshold: { enabled: true, warning: 70, critical: 90, direction: 'above' } },
})
const screen = (): ResolvedScreen => ({ id: 'index', name: '首页', dwell_seconds: 0, in_rotation: true, widgets: [widget('a'), widget('u', 'unconfigured')] })
const inst = (): ScreenInstanceData => ({
  instance_id: 'i1', display_state: 'ok', report_status: 'ok', report_stale: false, summary: '', last_success_at: null,
  items: [{ key: 'cpu', type: 'gauge', value: 50 }, { key: 'up', type: 'state', state: 'ok' }],
})

describe('预览状态', () => {
  it('实际：原样返回同一引用', () => {
    const s = screen(), d = { i1: inst() }
    const out = applyPreviewState(s, d, 'real')
    expect(out.screen).toBe(s)
    expect(out.data).toBe(d)
  })

  it('不修改入参（草稿不被污染）', () => {
    const s = screen(), d = { i1: inst() }
    const before = JSON.stringify([s, d])
    applyPreviewState(s, d, 'critical')
    expect(JSON.stringify([s, d])).toBe(before)
  })

  for (const level of ['ok', 'warning', 'critical'] as const) {
    it(`${level}：展示状态、state 项与阈值都落在该级别，任意读数同级`, () => {
      const out = applyPreviewState(screen(), { i1: inst() }, level)
      const w = out.screen.widgets[0]
      expect(w.display_state).toBe(level)
      expect(w.options.title).toBe('x')
      const thr = parseThreshold(w.options.threshold)
      for (const v of [-1e9, 0, 50, 100, 1e9]) expect(thresholdLevel(v, thr)).toBe(level)
      expect(out.data.i1!.display_state).toBe(level)
      expect(out.data.i1!.items.find((i) => i.key === 'up')!.state).toBe(level)
      expect(out.data.i1!.items.find((i) => i.key === 'cpu')!.value).toBe(50)
    })
  }

  it('未配置的小组件保持占位', () => {
    expect(applyPreviewState(screen(), {}, 'critical').screen.widgets[1].display_state).toBe('unconfigured')
  })
})
