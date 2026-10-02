import { describe, expect, it } from 'vitest'
import { applyOptionsPatch, buildThreshold, readThresholdForm } from './options'

describe('显示选项', () => {
  it('空字符串、null、undefined 都删键，title 永远不会是 null', () => {
    const out = applyOptionsPatch({ title: 'a', icon: 'cpu', text: 't' }, { title: '', icon: null, text: undefined })
    expect(out).toEqual({})
    expect(JSON.stringify(applyOptionsPatch({ title: 'a' }, { title: null }))).toBe('{}')
  })
  it('其余键原样合并，不改入参', () => {
    const src = { icon: 'cpu' }
    expect(applyOptionsPatch(src, { title: '新' })).toEqual({ icon: 'cpu', title: '新' })
    expect(src).toEqual({ icon: 'cpu' })
  })
  it('手动阈值固定为 {enabled, warning?, critical?, direction}，未填的阈值不写键', () => {
    expect(buildThreshold({ enabled: true, warning: 70, critical: 90, direction: 'above' })).toEqual({
      enabled: true, warning: 70, critical: 90, direction: 'above',
    })
    const partial = buildThreshold({ enabled: true, warning: 20, direction: 'below' })
    expect(partial).toEqual({ enabled: true, warning: 20, direction: 'below' })
    expect('critical' in partial).toBe(false)
    expect(buildThreshold({ enabled: false, warning: Number.NaN, direction: 'above' })).toEqual({ enabled: false, direction: 'above' })
  })
  it('读取：格式不对给关闭状态，往返一致', () => {
    expect(readThresholdForm(undefined)).toEqual({ enabled: false, direction: 'above' })
    expect(readThresholdForm('x')).toEqual({ enabled: false, direction: 'above' })
    const t = { enabled: true, warning: 1, critical: 2, direction: 'below' as const }
    expect(readThresholdForm(buildThreshold(t))).toEqual(t)
  })
})
