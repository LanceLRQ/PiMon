import { describe, expect, it } from 'vitest'
import { dwellMs, nextInRotation, type RotationScreen } from './carousel'

const s = (id: string, inRotation: boolean, dwellSeconds = 0): RotationScreen => ({ id, inRotation, dwellSeconds })

describe('轮播顺序', () => {
  const screens = [s('index', true), s('a', false), s('b', true), s('c', true)]

  it('按布局顺序取下一个参与轮播的 screen，末尾回到开头', () => {
    expect(nextInRotation(screens, 'index')).toBe('b')
    expect(nextInRotation(screens, 'b')).toBe('c')
    expect(nextInRotation(screens, 'c')).toBe('index')
  })

  it('当前 screen 不参与轮播时从它的位置往后找', () => {
    expect(nextInRotation(screens, 'a')).toBe('b')
  })

  it('只有当前一个参与轮播或没有参与轮播的时返回 null', () => {
    expect(nextInRotation([s('index', true), s('a', false)], 'index')).toBeNull()
    expect(nextInRotation([s('index', false), s('a', false)], 'index')).toBeNull()
    expect(nextInRotation([], 'index')).toBeNull()
  })

  it('当前 screen 不存在时取第一个参与轮播的', () => {
    expect(nextInRotation(screens, 'gone')).toBe('index')
  })
})

describe('停留时长', () => {
  it('每屏单独设置优先，0 用默认值', () => {
    expect(dwellMs(s('a', true, 30), 15)).toBe(30_000)
    expect(dwellMs(s('a', true, 0), 15)).toBe(15_000)
  })
})
