/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { DEFAULT_THEME_ID, getTheme, isThemeId, themes } from './registry'

// vitest 配置了 css:false，?raw 导入 CSS 会得到空串，所以直接用 fs 读文本
const read = (rel: string) => readFileSync(new URL(rel, import.meta.url), 'utf8')
const ambientCss = read('./ambient/tokens.css')
const missionControlCss = read('./mission-control/tokens.css')
const industrialCss = read('./industrial/tokens.css')
const effectsCss = read('./reduce-effects.css')
const indexCss = read('./index.css')

// 设计 10.2 的 L2 清单，名称逐字对应
const statusLevels = ['ok', 'warning', 'critical', 'unknown', 'error'] as const
const l2Tokens = [
  '--background', '--foreground', '--card', '--card-foreground', '--popover', '--popover-foreground',
  '--muted', '--muted-foreground', '--border', '--primary', '--primary-foreground', '--ring',
  '--surface-raised', '--foreground-faint',
  ...statusLevels.flatMap((s) => ['color', 'foreground', 'marker', 'weight'].map((k) => `--status-${s}-${k}`)),
  '--status-warning-bg', '--status-critical-bg',
  '--state-dim-opacity', '--state-placeholder-border',
  '--font-display', '--font-numeric', '--font-label', '--font-body',
  '--size-value-xl', '--size-value-lg', '--size-value-md', '--size-value-sm', '--size-label',
  '--radius', '--radius-card', '--border-card-width', '--border-card-style',
  '--grid-gap', '--card-pad', '--density',
  '--chart-1', '--chart-2', '--chart-3', '--chart-4', '--chart-5', '--chart-grid',
  '--effect-card-shadow', '--effect-glow', '--effect-backdrop',
  '--motion-duration',
]

const cssById: Record<string, string> = {
  ambient: ambientCss,
  'mission-control': missionControlCss,
  industrial: industrialCss,
}

type Decls = Map<string, string>

// 提取主题规则块：选择器必须是 :root[data-theme="x"], [data-theme="x"]
function parseTheme(id: string): { selector: string; decls: Decls } {
  const css = cssById[id].replace(/\/\*[\s\S]*?\*\//g, '')
  const m = /([^{}]+)\{([^{}]*)\}/.exec(css)
  if (!m) throw new Error(`${id} 没有规则块`)
  const decls: Decls = new Map()
  for (const d of m[2].matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) decls.set(d[1], d[2].trim().replace(/\s+/g, ' '))
  return { selector: m[1].trim().replace(/\s+/g, ' '), decls }
}

const hex = /^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$/

function resolve(decls: Decls, name: string, depth = 0): string {
  const v = decls.get(name)
  if (v === undefined) throw new Error(`未定义 ${name}`)
  const ref = /^var\((--[\w-]+)\)$/.exec(v)
  if (!ref) return v
  if (depth > 8) throw new Error(`var 链过深 ${name}`)
  return resolve(decls, ref[1], depth + 1)
}

function luminance(color: string): number {
  const n = parseInt(color.slice(1, 7), 16)
  const ch = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => {
    const s = c / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2]
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

// 对比度配对表：[前景, 底色, 下限, 用途]
const textPairs: [string, string, number][] = [
  // 正文 >= 4.5
  ['--foreground', '--background', 4.5],
  ['--foreground', '--surface-raised', 4.5],
  ['--card-foreground', '--card', 4.5],
  ['--popover-foreground', '--popover', 4.5],
  ['--muted-foreground', '--background', 4.5],
  ['--muted-foreground', '--card', 4.5],
  ['--muted-foreground', '--muted', 4.5],
  // Ruling 42：承载信息的小字（时间戳、单位、标签）用 muted-foreground，落在详情层与横幅底上也要够
  ['--muted-foreground', '--surface-raised', 4.5],
  ['--primary-foreground', '--primary', 4.5],
  ...statusLevels.map((s): [string, string, number] => [`--status-${s}-foreground`, `--status-${s}-color`, 4.5]),
  ['--foreground', '--status-warning-bg', 4.5],
  ['--foreground', '--status-critical-bg', 4.5],
  // 大号读数、图形与次要文字 >= 3
  ...statusLevels.map((s): [string, string, number] => [`--status-${s}-color`, '--card', 3]),
  ...statusLevels.map((s): [string, string, number] => [`--status-${s}-color`, '--background', 3]),
  ['--status-warning-color', '--status-warning-bg', 3],
  ['--status-critical-color', '--status-critical-bg', 3],
  ['--foreground-faint', '--background', 3],
  ['--foreground-faint', '--card', 3],
  ['--primary', '--card', 3],
  ['--chart-1', '--card', 3],
  ['--chart-2', '--card', 3],
  ['--chart-3', '--card', 3],
  ['--chart-4', '--card', 3],
  ['--chart-5', '--card', 3],
]

describe('主题注册表', () => {
  it('包含三个内置主题，默认 ambient', () => {
    expect(themes.map((t) => t.id)).toEqual(['ambient', 'mission-control', 'industrial'])
    expect(DEFAULT_THEME_ID).toBe('ambient')
    expect(isThemeId('industrial')).toBe(true)
    expect(isThemeId('nope')).toBe(false)
    expect(getTheme('nope').id).toBe('ambient')
  })

  it('能力档案符合设计 10.4', () => {
    const by = Object.fromEntries(themes.map((t) => [t.id, t]))
    expect(by.ambient).toMatchObject({ tone: 'dark', palette: 'full', radius: 'round', density: 'airy', effects: ['glow'] })
    expect(by['mission-control']).toMatchObject({ tone: 'dark', palette: 'full', radius: 'sharp', density: 'compact', effects: ['glow'] })
    expect(by.industrial).toMatchObject({ tone: 'light', palette: 'full', radius: 'soft', density: 'regular', effects: [] })
    for (const t of themes) {
      expect(t.nameKey).toBe(`screenThemes.${t.id}`)
      expect(t.name.zh).toBeTruthy()
      expect(t.name.en).toBeTruthy()
    }
  })

  it('能力档案的 density 与 tokens.css 的 --density 一致', () => {
    for (const t of themes) expect(parseTheme(t.id).decls.get('--density')).toBe(t.density)
  })
})

describe.each(themes.map((t) => t.id))('主题 %s 的 tokens.css', (id) => {
  const { selector, decls } = parseTheme(id)

  it('选择器同时支持根与子树', () => {
    expect(selector).toBe(`:root[data-theme="${id}"], [data-theme="${id}"]`)
  })

  it('定义了全部 L2 token', () => {
    const missing = l2Tokens.filter((n) => !decls.has(n))
    expect(missing).toEqual([])
  })

  it('不含 color-mix、颜色函数、远程字体与 backdrop-filter', () => {
    const css = cssById[id].replace(/\/\*[\s\S]*?\*\//g, '')
    expect(css).not.toMatch(/color-mix\(/)
    expect(css).not.toMatch(/\b(rgba?|hsla?|hwb|lab|lch|oklab|oklch)\(/)
    expect(css).not.toMatch(/@import\s+url|@font-face|https?:\/\//)
    expect(css).not.toMatch(/backdrop-filter|@keyframes|animation\s*:/)
  })

  it('L1 只用 hex，L2 颜色只引用 L1 或 hex', () => {
    for (const [name, value] of decls) {
      if (name.startsWith('--p-')) expect(value, name).toMatch(hex)
    }
    const colorTokens = l2Tokens.filter(
      (n) => /^--(background|foreground|card|card-foreground|popover|popover-foreground|muted|muted-foreground|border|primary|primary-foreground|ring|surface-raised|foreground-faint|chart-\d|chart-grid)$/.test(n)
        || /^--status-\w+-(color|foreground|bg)$/.test(n),
    )
    expect(colorTokens.length).toBeGreaterThan(30)
    for (const n of colorTokens) {
      const v = decls.get(n)!
      expect(v, n).toMatch(/^(#[0-9a-fA-F]{6,8}|var\(--p-[\w-]+\))$/)
      expect(resolve(decls, n), n).toMatch(hex)
    }
    // 非颜色型 token 里引用 var 只能是 --p-*
    for (const [name, value] of decls) {
      if (name.startsWith('--p-')) continue
      for (const ref of value.matchAll(/var\((--[\w-]+)\)/g)) {
        expect(ref[1], `${name} 引用了 ${ref[1]}`).toMatch(/^--(p-|radius$)/)
      }
    }
  })

  it('五级状态 marker 两两不同，且都是允许的取值', () => {
    const markers = statusLevels.map((s) => decls.get(`--status-${s}-marker`))
    expect(new Set(markers).size).toBe(5)
    for (const m of markers) expect(['dot', 'ring', 'square', 'triangle', 'diamond']).toContain(m)
  })

  it('critical 的 weight 为 strong 或 invert，其余取值合法', () => {
    expect(['strong', 'invert']).toContain(decls.get('--status-critical-weight'))
    for (const s of statusLevels) expect(['normal', 'strong', 'invert']).toContain(decls.get(`--status-${s}-weight`))
  })

  it('字体栈以 Noto Sans CJK SC 开头，使用等宽数字用的字体族', () => {
    for (const n of ['--font-display', '--font-label', '--font-body']) {
      expect(decls.get(n), n).toMatch(/^"Noto Sans CJK SC",/)
    }
    expect(decls.get('--font-numeric')).toMatch(/(monospace|sans-serif)$/)
  })

  it('--effect-backdrop 为 none，--motion-duration 为时长', () => {
    expect(decls.get('--effect-backdrop')).toBe('none')
    expect(decls.get('--motion-duration')).toMatch(/^\d+m?s$/)
  })

  it('文字与图形对比度达到 WCAG AA', () => {
    const failures: string[] = []
    for (const [fg, bg, min] of textPairs) {
      const ratio = contrast(resolve(decls, fg), resolve(decls, bg))
      if (ratio < min) failures.push(`${fg} / ${bg} = ${ratio.toFixed(2)} < ${min}`)
    }
    expect(failures).toEqual([])
  })
})

describe('屏幕命名空间颜色映射', () => {
  const css = read('./screen-colors.css')
  const entries = [...css.matchAll(/(--color-s-[\w-]+):\s*var\((--[\w-]+)\);/g)]

  it('每个 --color-s-* 都指向已定义的 L2 token', () => {
    expect(entries.length).toBeGreaterThanOrEqual(30)
    for (const [, name, ref] of entries) expect(l2Tokens, name).toContain(ref)
  })

  it('覆盖五级状态的颜色、前景与全部图表色', () => {
    const names = entries.map((e) => e[1])
    for (const s of ['ok', 'warning', 'critical', 'unknown', 'error']) {
      expect(names).toContain(`--color-s-${s}`)
      expect(names).toContain(`--color-s-${s}-fg`)
    }
    for (let i = 1; i <= 5; i++) expect(names).toContain(`--color-s-chart-${i}`)
  })

  it('i18n 主题名两种语言都有', () => {
    for (const t of themes) {
      for (const lang of ['zh', 'en'] as const) {
        const json = JSON.parse(read(`../i18n/${lang}.json`)) as { screenThemes: Record<string, string> }
        expect(json.screenThemes[t.id], `${lang}.${t.id}`).toBe(t.name[lang])
      }
    }
  })
})

describe('降低特效与聚合入口', () => {
  it('reduce-effects.css 把 effect 取 none、动效时长取 0ms', () => {
    const css = effectsCss.replace(/\/\*[\s\S]*?\*\//g, '')
    expect(css).toContain('[data-reduce-effects]')
    for (const n of ['--effect-card-shadow', '--effect-glow', '--effect-backdrop']) {
      expect(css).toMatch(new RegExp(`${n}:\\s*none`))
    }
    expect(css).toMatch(/--motion-duration:\s*0ms/)
  })

  it('同一元素与祖先两种写法都被覆盖', () => {
    const css = effectsCss.replace(/\/\*[\s\S]*?\*\//g, '')
    expect(css).toContain('[data-theme][data-reduce-effects]')
    expect(css).toContain('[data-reduce-effects] [data-theme]')
  })

  it('index.css 聚合三个主题与降低特效，且降低特效在最后', () => {
    const imports = [...indexCss.matchAll(/@import\s+["']([^"']+)["']/g)].map((m) => m[1])
    expect(imports).toEqual([
      './ambient/tokens.css',
      './mission-control/tokens.css',
      './industrial/tokens.css',
      './reduce-effects.css',
    ])
  })
})
