import { useSyncExternalStore } from 'react'

export type ThemeChoice = 'light' | 'dark' | 'system'

export const themeStorageKey = 'pimon.admin.theme'

const listeners = new Set<() => void>()
let current: ThemeChoice = 'light'
let stopSystemWatch: (() => void) | null = null

function isChoice(v: unknown): v is ThemeChoice {
  return v === 'light' || v === 'dark' || v === 'system'
}

// 读取失败或值非法一律回落浅色（设计：默认浅色）
export function readThemeChoice(): ThemeChoice {
  try {
    const v = localStorage.getItem(themeStorageKey)
    return isChoice(v) ? v : 'light'
  } catch {
    return 'light'
  }
}

function systemPrefersDark(): boolean {
  return typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches
}

function applyTheme(choice: ThemeChoice) {
  const dark = choice === 'dark' || (choice === 'system' && systemPrefersDark())
  document.documentElement.classList.toggle('dark', dark)
}

// 选择为「跟随系统」时监听系统偏好变化，其余情况不监听
function watchSystem(choice: ThemeChoice) {
  stopSystemWatch?.()
  stopSystemWatch = null
  if (choice !== 'system' || typeof matchMedia !== 'function') return
  const mq = matchMedia('(prefers-color-scheme: dark)')
  const onChange = () => applyTheme('system')
  mq.addEventListener('change', onChange)
  stopSystemWatch = () => mq.removeEventListener('change', onChange)
}

function commit(choice: ThemeChoice) {
  current = choice
  applyTheme(choice)
  watchSystem(choice)
  listeners.forEach((l) => l())
}

// 启动时调用：按存储的选择应用主题（index.html 的内联脚本负责首帧，这里接管后续逻辑）
export function initTheme() {
  commit(readThemeChoice())
}

export function setThemeChoice(choice: ThemeChoice) {
  try {
    localStorage.setItem(themeStorageKey, choice)
  } catch {
    // 存储不可用时仅本次生效
  }
  commit(choice)
}

export function getThemeChoice(): ThemeChoice {
  return current
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}

export function useThemeChoice(): ThemeChoice {
  return useSyncExternalStore(subscribe, getThemeChoice, getThemeChoice)
}
