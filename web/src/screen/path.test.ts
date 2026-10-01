import { describe, expect, it } from 'vitest'
import { isScreenPath } from './path'

describe('屏幕端路径判断', () => {
  it.each([
    ['/screen', true],
    ['/screen/', true],
    ['/screen/auth', true],
    ['/screens', false],
    ['/screens/editor', false],
    ['/', false],
    ['/settings', false],
  ])('%s → %s', (p, want) => {
    expect(isScreenPath(p)).toBe(want)
  })
})
