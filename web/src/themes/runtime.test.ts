import { afterEach, describe, expect, it, vi } from 'vitest'
import { readThemeRuntime, watchThemeRuntime } from './runtime'

function el(vars: Record<string, string>, attrs: Record<string, string> = {}) {
  const e = document.createElement('div')
  for (const [k, v] of Object.entries(vars)) e.style.setProperty(k, v)
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v)
  document.body.appendChild(e)
  return e
}

const base = {
  '--status-ok-marker': ' dot ', '--status-ok-weight': 'normal',
  '--status-warning-marker': 'ring', '--status-warning-weight': 'normal',
  '--status-critical-marker': 'triangle', '--status-critical-weight': 'invert',
  '--status-unknown-marker': 'diamond', '--status-unknown-weight': 'normal',
  '--status-error-marker': 'square', '--status-error-weight': 'strong',
  '--density': 'compact',
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('readThemeRuntime', () => {
  it('从计算样式读出五级 marker/weight 与密度', () => {
    const e = el(base, { 'data-theme': 'industrial' })
    const rt = readThemeRuntime(e)
    expect(rt.themeId).toBe('industrial')
    expect(rt.density).toBe('compact')
    expect(rt.reduceEffects).toBe(false)
    expect(rt.status.ok).toEqual({ marker: 'dot', weight: 'normal' })
    expect(rt.status.critical).toEqual({ marker: 'triangle', weight: 'invert' })
    expect(rt.status.error).toEqual({ marker: 'square', weight: 'strong' })
  })

  it('取不到或取值非法时回退到安全默认并保持五级互异', () => {
    const rt = readThemeRuntime(el({ '--status-ok-marker': 'bogus' }))
    expect(rt.status.ok.marker).toBe('dot')
    expect(rt.status.critical.weight).toBe('strong')
    expect(new Set(Object.values(rt.status).map((s) => s.marker)).size).toBe(5)
    expect(rt.density).toBe('regular')
  })

  it('识别降低特效属性，并沿祖先查找 data-theme', () => {
    const parent = el(base, { 'data-theme': 'ambient', 'data-reduce-effects': '' })
    const child = document.createElement('span')
    parent.appendChild(child)
    const rt = readThemeRuntime(child)
    expect(rt.themeId).toBe('ambient')
    expect(rt.reduceEffects).toBe(true)
  })
})

describe('watchThemeRuntime', () => {
  it('data-theme 改变时回调新读数，取消后不再回调', async () => {
    const e = el(base, { 'data-theme': 'ambient' })
    const cb = vi.fn()
    const stop = watchThemeRuntime(e, cb)
    e.setAttribute('data-theme', 'industrial')
    await new Promise((r) => setTimeout(r, 0))
    expect(cb).toHaveBeenCalledTimes(1)
    expect(cb.mock.calls[0][0].themeId).toBe('industrial')
    stop()
    e.setAttribute('data-theme', 'ambient')
    await new Promise((r) => setTimeout(r, 0))
    expect(cb).toHaveBeenCalledTimes(1)
  })
})
