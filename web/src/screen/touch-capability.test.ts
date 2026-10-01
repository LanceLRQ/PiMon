import { afterEach, describe, expect, it, vi } from 'vitest'
import { hasTouch } from './touch-capability'

afterEach(() => vi.unstubAllGlobals())

function stubCoarse(matches: boolean) {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('any-pointer: coarse') ? matches : false, addEventListener() {}, removeEventListener() {} }))
}

describe('触摸能力判断（设计 5.5b）', () => {
  it('设置为有触摸或无触摸时直接采用', () => {
    stubCoarse(false)
    expect(hasTouch('touch')).toBe(true)
    stubCoarse(true)
    expect(hasTouch('none')).toBe(false)
  })

  it('自动检测参考 any-pointer: coarse', () => {
    stubCoarse(true)
    expect(hasTouch('auto')).toBe(true)
    stubCoarse(false)
    expect(hasTouch('auto')).toBe(false)
  })

  it('没有 matchMedia 时自动检测按无触摸处理', () => {
    vi.stubGlobal('matchMedia', undefined)
    expect(hasTouch('auto')).toBe(false)
  })
})
