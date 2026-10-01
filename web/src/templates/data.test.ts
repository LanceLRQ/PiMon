import { describe, expect, it } from 'vitest'
import type { Item } from '@/types/generated'
import { findItem, levelOfValue, readNumber, resolveValueLevel, thresholdLevel } from './data'
import { bound } from './test-utils'

describe('thresholdLevel', () => {
  it('above：达到阈值即升级', () => {
    const t = { enabled: true, warning: 70, critical: 90, direction: 'above' as const }
    expect(thresholdLevel(50, t)).toBe('ok')
    expect(thresholdLevel(70, t)).toBe('warning')
    expect(thresholdLevel(95, t)).toBe('critical')
  })
  it('below：低于阈值升级', () => {
    const t = { enabled: true, warning: 20, critical: 10, direction: 'below' as const }
    expect(thresholdLevel(50, t)).toBe('ok')
    expect(thresholdLevel(15, t)).toBe('warning')
    expect(thresholdLevel(5, t)).toBe('critical')
  })
  it('未开启或没有任何阈值时不判级', () => {
    expect(thresholdLevel(99, { enabled: false, critical: 1, direction: 'above' })).toBeNull()
    expect(thresholdLevel(99, { enabled: true, direction: 'above' })).toBeNull()
    expect(thresholdLevel(99, undefined)).toBeNull()
  })
})

describe('readNumber / findItem', () => {
  const g: Item = { key: 'cpu', type: 'gauge', value: 0, unit: '%' }
  it('值为 0 是 0，缺失是 null', () => {
    expect(readNumber(g)).toBe(0)
    expect(readNumber({ key: 'a', type: 'gauge' })).toBeNull()
    expect(readNumber({ key: 'a', type: 'gauge', value: Number.NaN })).toBeNull()
  })
  it('按类型取默认字段，ref.field 可覆盖', () => {
    expect(readNumber({ key: 'm', type: 'money', amount: 12.5, currency: 'USD' })).toBe(12.5)
    expect(readNumber({ key: 'q', type: 'quota', remaining_pct: 40, used: 6 })).toBe(40)
    expect(readNumber({ key: 'q', type: 'quota', remaining_pct: 40, used: 6 }, 'used')).toBe(6)
  })
  it('findItem 按 ref 在实例数据里找数据项', () => {
    const { widget, data } = bound('value', { cols: 1, rows: 1 }, [g])
    expect(findItem(widget.slots.value?.[0], data)?.key).toBe('cpu')
    expect(findItem({ instance_id: 'zz', item: 'cpu' }, data)).toBeUndefined()
    expect(findItem(undefined, data)).toBeUndefined()
  })
})

describe('resolveValueLevel（Ruling 32）', () => {
  const num: Item = { key: 'n', type: 'number', value: 85 }
  it('手动阈值优先于默认阈值', () => {
    const manual = { enabled: true, warning: 50, critical: 80, direction: 'above' as const }
    const dflt = { enabled: true, warning: 95, critical: 99, direction: 'above' as const }
    expect(resolveValueLevel(num, undefined, manual, dflt)).toBe('critical')
    expect(resolveValueLevel(num, undefined, undefined, dflt)).toBe('ok')
  })
  it('手动阈值未开启时回落到默认阈值', () => {
    const dflt = { enabled: true, warning: 80, direction: 'above' as const }
    expect(resolveValueLevel(num, undefined, { enabled: false, critical: 1, direction: 'above' }, dflt)).toBe('warning')
  })
  it('state 项按其 state，其余数值项无阈值时为中性', () => {
    expect(resolveValueLevel({ key: 's', type: 'state', state: 'warning' }, undefined, undefined, undefined)).toBe('warning')
    expect(resolveValueLevel(num, undefined, undefined, undefined)).toBeNull()
    expect(resolveValueLevel(undefined, undefined, undefined, undefined)).toBeNull()
  })
  it('levelOfValue 把非法状态当作 unknown', () => {
    expect(levelOfValue('critical')).toBe('critical')
    expect(levelOfValue('whatever')).toBe('unknown')
    expect(levelOfValue(undefined)).toBe('unknown')
  })
})
