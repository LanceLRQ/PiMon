/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import {
  checkGrid,
  evaluatePlacement,
  firstFreeSpot,
  moveByKey,
  occupiedCells,
  outOfBoundsWidgets,
  parseSizeKey,
  sizeAllowed,
  snapToCell,
  type GridProblem,
  type GridWidgetRect,
} from './grid-ops'

// Ruling 6：与 Go 测试读同一份 fixture，规则不得两端各写一份
interface FixtureCase {
  name: string
  grid: { cols: number; rows: number }
  widgets: (GridWidgetRect & { allowed?: string[] })[]
  problems: GridProblem[]
}
// 路径放进变量：字面量会被 vite 当作资源引用改写
const fixturePath = '../../../src/internal/hub/screens/testdata/grid_cases.json'
const fixture = JSON.parse(readFileSync(new URL(fixturePath, import.meta.url), 'utf8')) as { cases: FixtureCase[] }

const norm = (list: GridProblem[]) =>
  list
    .map((p) => ({ widget: p.widget, code: p.code, ...(p.with ? { with: p.with } : {}) }))
    .sort((a, b) => `${a.widget}|${a.code}|${a.with ?? ''}`.localeCompare(`${b.widget}|${b.code}|${b.with ?? ''}`))

describe('checkGrid 共享 fixture', () => {
  it('fixture 非空', () => expect(fixture.cases.length).toBeGreaterThan(10))
  for (const c of fixture.cases) {
    it(c.name, () => {
      expect(norm(checkGrid(c.grid, c.widgets))).toEqual(norm(c.problems))
    })
  }
})

const grid = { cols: 6, rows: 4 }
const w = (id: string, col: number, row: number, wd = 1, h = 1): GridWidgetRect => ({ id, col, row, w: wd, h })

describe('尺寸白名单', () => {
  it('解析与匹配', () => {
    expect(parseSizeKey('2x1')).toEqual({ cols: 2, rows: 1 })
    expect(parseSizeKey('abc')).toBeNull()
    expect(sizeAllowed([{ cols: 2, rows: 1 }], { cols: 2, rows: 1 })).toBe(true)
    expect(sizeAllowed([{ cols: 2, rows: 1 }], { cols: 1, rows: 2 })).toBe(false)
    expect(sizeAllowed(undefined, { cols: 9, rows: 9 })).toBe(true)
  })
})

describe('吸附', () => {
  const m = { cols: 6, rows: 4, cellW: 100, cellH: 100 }
  it('按最近的格吸附', () => {
    expect(snapToCell(149, 51, m, { cols: 1, rows: 1 })).toEqual({ col: 1, row: 1 })
    expect(snapToCell(151, 49, m, { cols: 1, rows: 1 })).toEqual({ col: 2, row: 0 })
  })
  it('钳制在网格内', () => {
    expect(snapToCell(-80, -80, m, { cols: 2, rows: 2 })).toEqual({ col: 0, row: 0 })
    expect(snapToCell(900, 900, m, { cols: 2, rows: 2 })).toEqual({ col: 4, row: 2 })
  })
  it('度量为 0 时回到原点', () => {
    expect(snapToCell(50, 50, { cols: 6, rows: 4, cellW: 0, cellH: 0 }, { cols: 1, rows: 1 })).toEqual({ col: 0, row: 0 })
  })
})

describe('evaluatePlacement', () => {
  it('空位可放', () => {
    expect(evaluatePlacement(grid, [w('a', 0, 0)], w('n', 1, 0, 2, 2))).toEqual({ ok: true, conflicts: [] })
  })
  it('撞到别人给出冲突 id，忽略自己', () => {
    const others = [w('a', 0, 0, 2, 2), w('b', 4, 0)]
    expect(evaluatePlacement(grid, others, w('a', 1, 0, 2, 2))).toEqual({ ok: true, conflicts: [] })
    expect(evaluatePlacement(grid, others, w('n', 1, 1, 2, 2))).toEqual({ ok: false, reason: 'overlap', conflicts: ['a'] })
  })
  it('越界优先报告', () => {
    expect(evaluatePlacement(grid, [], w('n', 5, 0, 2, 1))).toEqual({ ok: false, reason: 'out_of_bounds', conflicts: [] })
    expect(evaluatePlacement(grid, [], w('n', -1, 0))).toMatchObject({ ok: false, reason: 'out_of_bounds' })
  })
})

describe('方向键移动', () => {
  it('按格移动一步', () => {
    expect(moveByKey('ArrowRight', grid, [], w('a', 1, 1))).toEqual({ col: 2, row: 1 })
    expect(moveByKey('ArrowLeft', grid, [], w('a', 1, 1))).toEqual({ col: 0, row: 1 })
    expect(moveByKey('ArrowDown', grid, [], w('a', 1, 1))).toEqual({ col: 1, row: 2 })
    expect(moveByKey('ArrowUp', grid, [], w('a', 1, 1))).toEqual({ col: 1, row: 0 })
  })
  it('到边界或撞到其他小组件时不动', () => {
    expect(moveByKey('ArrowLeft', grid, [], w('a', 0, 0))).toBeNull()
    expect(moveByKey('ArrowRight', grid, [], w('a', 5, 0))).toBeNull()
    expect(moveByKey('ArrowRight', grid, [w('b', 2, 0)], w('a', 1, 0))).toBeNull()
  })
  it('非方向键返回 null', () => {
    expect(moveByKey('Enter', grid, [], w('a', 1, 1))).toBeNull()
  })
})

describe('其他纯函数', () => {
  it('firstFreeSpot 按行优先找空位，放不下返回 null', () => {
    expect(firstFreeSpot(grid, [w('a', 0, 0, 6, 1)], { cols: 2, rows: 1 })).toEqual({ col: 0, row: 1 })
    expect(firstFreeSpot({ cols: 2, rows: 2 }, [w('a', 0, 0, 2, 2)], { cols: 1, rows: 1 })).toBeNull()
  })
  it('缩小网格的越界列表', () => {
    const list = [w('ok', 0, 0, 2, 1), w('r', 3, 0, 2, 1), w('b', 0, 2, 2, 2)]
    expect(outOfBoundsWidgets({ cols: 4, rows: 3 }, list)).toEqual(['r', 'b'])
  })
  it('占用格数', () => {
    expect(occupiedCells(grid, [w('a', 0, 0, 2, 2), w('b', 5, 3)])).toBe(5)
    expect(occupiedCells(grid, [w('a', 5, 3, 3, 3)])).toBe(1)
  })
})
