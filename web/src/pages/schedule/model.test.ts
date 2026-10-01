import { describe, expect, it } from 'vitest'
import type { Schedule } from '@/types/generated'
import {
  currentAndNext,
  formatHM,
  fromServer,
  lengthOf,
  minuteInZone,
  moveBoundary,
  parseHM,
  removePeriod,
  setEnd,
  setStart,
  setTheme,
  splitAt,
  splitMid,
  themeUsage,
  toServer,
  validate,
  type DraftPeriod,
} from './model'

const plan = (...p: [string, string, string][]): Schedule => ({ periods: p.map(([start, end, theme]) => ({ start, end, theme })) })
const base = (): DraftPeriod[] => fromServer(plan(['07:00', '19:00', 'industrial'], ['19:00', '23:00', 'ambient'], ['23:00', '07:00', 'off']))
const pairs = (ps: DraftPeriod[]) => toServer(ps).periods.map((p) => `${p.start}-${p.end}-${p.theme}`)

describe('时间解析与格式化', () => {
  it('严格解析 HH:MM，24:00 视为一天结束', () => {
    expect(parseHM('07:05')).toBe(425)
    expect(parseHM('7:05')).toBe(425)
    expect(parseHM('23:59')).toBe(1439)
    expect(parseHM('24:00')).toBe(0)
    for (const bad of ['', '24:01', '12:60', 'ab:cd', '1234', '12:5']) expect(parseHM(bad)).toBeNull()
  })
  it('格式化补零，时长按跨日与全天计算', () => {
    expect(formatHM(425)).toBe('07:05')
    const ps = base()
    expect(lengthOf(ps[2])).toBe(8 * 60)
    expect(lengthOf(fromServer(plan(['00:00', '00:00', 'ambient']))[0])).toBe(1440)
  })
})

describe('校验与后端一致', () => {
  it('合法计划无问题', () => {
    expect(validate(base())).toEqual([])
  })
  it('缺口、重叠按区间报告', () => {
    const gap = fromServer(plan(['00:00', '08:00', 'ambient'], ['10:00', '00:00', 'off']))
    expect(validate(gap)).toEqual([{ kind: 'gap', from: '08:00', to: '10:00' }])
    const overlap = fromServer(plan(['00:00', '12:00', 'ambient'], ['10:00', '00:00', 'off']))
    expect(validate(overlap)).toEqual([{ kind: 'overlap', from: '10:00', to: '12:00' }])
  })
  it('空计划、未知主题、时间格式错误', () => {
    expect(validate([])).toEqual([{ kind: 'empty' }])
    const bad = fromServer(plan(['00:00', '00:00', 'neon']))
    expect(validate(bad)).toEqual([{ kind: 'theme', period: 0 }])
    const fmt: DraftPeriod[] = [{ key: 'x', start: NaN, end: 0, theme: 'ambient' }]
    expect(validate(fmt)).toEqual([{ kind: 'format', period: 0 }])
  })
})

describe('编辑联动', () => {
  it('改开始时间同时让前一段的结束跟随，保持首尾相接', () => {
    const ps = base()
    const next = setStart(ps, ps[1].key, parseHM('20:00')!)
    expect(pairs(next)).toEqual(['07:00-20:00-industrial', '20:00-23:00-ambient', '23:00-07:00-off'])
    expect(validate(next)).toEqual([])
  })
  it('改结束时间同时让后一段的开始跟随', () => {
    const ps = base()
    const next = setEnd(ps, ps[0].key, parseHM('18:30')!)
    expect(pairs(next)).toEqual(['07:00-18:30-industrial', '18:30-23:00-ambient', '23:00-07:00-off'])
  })
  it('跨日段的结束改动绕过午夜跟随到下一段', () => {
    const ps = base()
    const next = setEnd(ps, ps[2].key, parseHM('06:00')!)
    expect(pairs(next)).toEqual(['06:00-19:00-industrial', '19:00-23:00-ambient', '23:00-06:00-off'])
  })
  it('开始时间越过其他段后重新按开始时刻排序，并由校验报出重叠', () => {
    const ps = base()
    const next = setStart(ps, ps[0].key, parseHM('20:00')!)
    expect(validate(next).length).toBeGreaterThan(0)
    expect(next.map((p) => p.start)).toEqual([...next.map((p) => p.start)].sort((a, b) => a - b))
  })
  it('只有一个全天段时起止互相带动，仍是全天', () => {
    const one = fromServer(plan(['00:00', '00:00', 'ambient']))
    const next = setStart(one, one[0].key, parseHM('06:00')!)
    expect(pairs(next)).toEqual(['06:00-06:00-ambient'])
    expect(validate(next)).toEqual([])
  })
  it('换主题只改该段', () => {
    const ps = base()
    expect(pairs(setTheme(ps, ps[1].key, 'mission-control'))[1]).toBe('19:00-23:00-mission-control')
  })
})

describe('拆分与删除', () => {
  it('在某时刻新增时段会拆分它落入的那一段，计划仍覆盖全天', () => {
    const ps = base()
    const r = splitAt(ps, parseHM('21:00')!, 'mission-control')!
    expect(pairs(r.periods)).toEqual(['07:00-19:00-industrial', '19:00-21:00-ambient', '21:00-23:00-mission-control', '23:00-07:00-off'])
    expect(r.newKey).toBe(r.periods[2].key)
    expect(validate(r.periods)).toEqual([])
  })
  it('拆分跨日段：落在午夜之后的部分也能找到所在段', () => {
    const ps = base()
    const r = splitAt(ps, parseHM('03:00')!, 'ambient')!
    expect(pairs(r.periods)).toEqual(['03:00-07:00-ambient', '07:00-19:00-industrial', '19:00-23:00-ambient', '23:00-03:00-off'])
    expect(validate(r.periods)).toEqual([])
  })
  it('落在某段起点上或段太短时无法拆分', () => {
    const ps = base()
    expect(splitAt(ps, parseHM('19:00')!, 'ambient')).toBeNull()
    const tiny = fromServer(plan(['00:00', '00:01', 'off'], ['00:01', '00:00', 'ambient']))
    expect(splitAt(tiny, 0, 'ambient')).toBeNull()
  })
  it('拆分选中段取中点；关屏段拆出的新段仍是关屏', () => {
    const ps = base()
    const r = splitMid(ps, ps[1].key)!
    expect(pairs(r.periods)).toEqual(['07:00-19:00-industrial', '19:00-21:00-ambient', '21:00-23:00-mission-control', '23:00-07:00-off'])
    const off = splitMid(ps, ps[2].key)!
    expect(pairs(off.periods)[0]).toBe('03:00-07:00-off')
  })
  it('只有一个全天段也能拆（取半天）', () => {
    const one = fromServer(plan(['00:00', '00:00', 'ambient']))
    const r = splitMid(one, one[0].key)!
    expect(pairs(r.periods)).toEqual(['00:00-12:00-ambient', '12:00-00:00-mission-control'])
  })
  it('删除时段由前一段延长到被删段的结束；只剩一段时不能删', () => {
    const ps = base()
    expect(pairs(removePeriod(ps, ps[1].key))).toEqual(['07:00-23:00-industrial', '23:00-07:00-off'])
    const two = fromServer(plan(['00:00', '12:00', 'ambient'], ['12:00', '00:00', 'off']))
    const left = removePeriod(two, two[1].key)
    expect(pairs(left)).toEqual(['00:00-00:00-ambient'])
    expect(removePeriod(left, left[0].key)).toBe(left)
  })
})

describe('时间轴边界移动', () => {
  it('把某段起点的边界移动 delta 分钟，前一段随之伸缩', () => {
    const ps = base()
    expect(pairs(moveBoundary(ps, ps[1].key, 15))).toEqual(['07:00-19:15-industrial', '19:15-23:00-ambient', '23:00-07:00-off'])
  })
  it('夹在两段各至少 1 分钟之内', () => {
    const ps = base()
    const far = moveBoundary(ps, ps[1].key, 24 * 60)
    expect(far[1].start).toBe(parseHM('22:59')!)
    expect(validate(far)).toEqual([])
    const one = fromServer(plan(['00:00', '00:00', 'ambient']))
    expect(moveBoundary(one, one[0].key, 5)).toBe(one)
  })
})

describe('服务端时区的现在与下一次变化', () => {
  const instant = Date.parse('2026-10-01T14:47:00Z')
  it('按设置时区取分钟数，不用浏览器本地时区', () => {
    expect(formatHM(minuteInZone(instant, 'Asia/Shanghai'))).toBe('22:47')
    expect(formatHM(minuteInZone(instant, 'UTC'))).toBe('14:47')
    expect(formatHM(minuteInZone(instant, 'America/Los_Angeles'))).toBe('07:47')
  })
  it('时区非法时回退 UTC', () => {
    expect(formatHM(minuteInZone(instant, 'Nope/Zone'))).toBe('14:47')
  })
  it('当前时段与下一次变化（同主题相邻段不算变化），跨午夜', () => {
    const ps = base()
    const now = minuteInZone(instant, 'Asia/Shanghai')
    const r = currentAndNext(ps, now)
    expect(r.current?.theme).toBe('ambient')
    expect(r.next).toEqual({ at: parseHM('23:00')!, theme: 'off', inMinutes: 13 })
    const night = currentAndNext(ps, parseHM('23:30')!)
    expect(night.next).toEqual({ at: parseHM('07:00')!, theme: 'industrial', inMinutes: 7 * 60 + 30 })
    const same = fromServer(plan(['00:00', '12:00', 'ambient'], ['12:00', '00:00', 'ambient']))
    expect(currentAndNext(same, 100).next).toBeNull()
  })
  it('各主题的使用时段', () => {
    expect(themeUsage(base())).toEqual({ industrial: ['07:00–19:00'], ambient: ['19:00–23:00'], off: ['23:00–07:00'] })
  })
})
