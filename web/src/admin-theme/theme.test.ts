import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { initTheme, readThemeChoice, setThemeChoice, themeStorageKey } from './theme'

function mockSystemDark(dark: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: dark && query.includes('dark'),
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }))
}

const isDark = () => document.documentElement.classList.contains('dark')

describe('管理界面主题', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
    mockSystemDark(false)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('首次打开是浅色', () => {
    initTheme()
    expect(readThemeChoice()).toBe('light')
    expect(isDark()).toBe(false)
  })

  it('切到深色后刷新仍是深色', () => {
    initTheme()
    setThemeChoice('dark')
    expect(isDark()).toBe(true)
    expect(localStorage.getItem(themeStorageKey)).toBe('dark')
    document.documentElement.classList.remove('dark') // 模拟刷新：页面状态丢失，存储保留
    initTheme()
    expect(isDark()).toBe(true)
  })

  it('清空存储后回到浅色', () => {
    setThemeChoice('dark')
    localStorage.clear()
    initTheme()
    expect(isDark()).toBe(false)
  })

  it('localStorage 不可用时是浅色，且切换不抛错', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    initTheme()
    expect(isDark()).toBe(false)
    expect(() => setThemeChoice('dark')).not.toThrow()
    expect(isDark()).toBe(true)
  })

  it('跟随系统时按系统偏好决定深浅', () => {
    mockSystemDark(true)
    setThemeChoice('system')
    expect(isDark()).toBe(true)
    mockSystemDark(false)
    initTheme()
    expect(isDark()).toBe(false)
  })

  it('存储里是未知值时回落浅色', () => {
    localStorage.setItem(themeStorageKey, 'purple')
    expect(readThemeChoice()).toBe('light')
  })
})
