/// <reference types="node" />
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

// 防闪烁内联脚本（Ruling 11）：直接取 index.html 里的脚本在 jsdom 中执行，验证按路径分支的行为
const html = readFileSync(path.resolve(import.meta.dirname, '../../index.html'), 'utf8')
const script = /<script>([\s\S]*?)<\/script>/.exec(html)![1]

function runAt(pathname: string) {
  window.history.pushState({}, '', pathname)
  document.documentElement.removeAttribute('data-theme')
  document.documentElement.classList.remove('dark')
  ;(0, eval)(script)
}

beforeEach(() => localStorage.clear())
afterEach(() => {
  localStorage.clear()
  window.history.pushState({}, '', '/')
})

describe('index.html 防闪烁脚本', () => {
  it('/screen 下按 pimon.screen.theme 设置 data-theme', () => {
    localStorage.setItem('pimon.screen.theme', 'industrial')
    runAt('/screen')
    expect(document.documentElement.getAttribute('data-theme')).toBe('industrial')
    runAt('/screen/auth')
    expect(document.documentElement.getAttribute('data-theme')).toBe('industrial')
  })

  it('/screen 下缺失或非法的取值用 ambient', () => {
    runAt('/screen')
    expect(document.documentElement.getAttribute('data-theme')).toBe('ambient')
    localStorage.setItem('pimon.screen.theme', 'neon')
    runAt('/screen')
    expect(document.documentElement.getAttribute('data-theme')).toBe('ambient')
  })

  it('/screen 下不加 .dark，即使管理端选了深色', () => {
    localStorage.setItem('pimon.admin.theme', 'dark')
    localStorage.setItem('pimon.screen.theme', 'mission-control')
    runAt('/screen')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.getAttribute('data-theme')).toBe('mission-control')
  })

  it('管理端页面按 pimon.admin.theme 加 .dark，且不设 data-theme', () => {
    localStorage.setItem('pimon.admin.theme', 'dark')
    localStorage.setItem('pimon.screen.theme', 'industrial')
    runAt('/settings')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  })

  it('/screens 之类以 screen 开头的管理端路径不算屏幕端', () => {
    localStorage.setItem('pimon.admin.theme', 'dark')
    runAt('/screens/editor')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
  })

  it('浅色与缺失保持浅色', () => {
    runAt('/')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    localStorage.setItem('pimon.admin.theme', 'light')
    runAt('/')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
