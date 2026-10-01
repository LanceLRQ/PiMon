import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { applyScreenTheme, persistScreenTheme, readStoredScreenTheme, screenThemeStorageKey } from './theme-apply'

beforeEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
  document.documentElement.removeAttribute('data-reduce-effects')
})
afterEach(() => localStorage.clear())

describe('屏幕主题应用', () => {
  it('在根元素上设置 data-theme；降低特效时在同一元素上设置 data-reduce-effects', () => {
    applyScreenTheme('industrial', true)
    const root = document.documentElement
    expect(root.getAttribute('data-theme')).toBe('industrial')
    expect(root.hasAttribute('data-reduce-effects')).toBe(true)
    applyScreenTheme('mission-control', false)
    expect(root.getAttribute('data-theme')).toBe('mission-control')
    expect(root.hasAttribute('data-reduce-effects')).toBe(false)
  })

  it('不会给根元素加管理端的 dark 类', () => {
    applyScreenTheme('ambient', false)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('未知主题回落到默认主题', () => {
    applyScreenTheme('nope', false)
    expect(document.documentElement.getAttribute('data-theme')).toBe('ambient')
  })

  it('主题写回 localStorage，读取时校验取值', () => {
    persistScreenTheme('industrial')
    expect(localStorage.getItem(screenThemeStorageKey)).toBe('industrial')
    expect(readStoredScreenTheme()).toBe('industrial')
    localStorage.setItem(screenThemeStorageKey, 'garbage')
    expect(readStoredScreenTheme()).toBe('ambient')
  })

  it('存储不可用时不抛错', () => {
    const orig = Storage.prototype.setItem
    Storage.prototype.setItem = () => {
      throw new Error('denied')
    }
    try {
      expect(() => persistScreenTheme('ambient')).not.toThrow()
    } finally {
      Storage.prototype.setItem = orig
    }
  })
})
