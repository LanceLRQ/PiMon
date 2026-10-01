import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { ESLint } from 'eslint'
import stylelint from 'stylelint'
import { describe, expect, it } from 'vitest'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const fixture = (name: string) => readFileSync(path.join(root, 'lint-fixtures/cases', name), 'utf8')

async function lintTs(code: string, filePath: string) {
  const eslint = new ESLint({ cwd: root, ignore: false })
  const [result] = await eslint.lintText(code, { filePath: path.join(root, filePath) })
  return result.messages.filter((m) => m.ruleId === 'no-restricted-syntax')
}

async function lintCss(code: string, codeFilename: string) {
  const result = await stylelint.lint({ code, codeFilename: path.join(root, codeFilename), cwd: root })
  const themeRules = new Set([
    'color-no-hex',
    'function-disallowed-list',
    'declaration-property-value-disallowed-list',
    'property-disallowed-list',
  ])
  return result.results.flatMap((r) => r.warnings).filter((w) => themeRules.has(w.rule))
}

describe('屏幕端与模板目录的样式约束（ESLint）', () => {
  it('颜色字面量在 screen 目录下报错', async () => {
    const msgs = await lintTs(fixture('color-literal.tsx'), 'src/screen/bad.tsx')
    expect(msgs.length).toBeGreaterThanOrEqual(4)
  })

  it('--p-* 引用在 templates 目录下报错', async () => {
    const msgs = await lintTs(fixture('primitive-token.tsx'), 'src/templates/bad.tsx')
    expect(msgs.length).toBeGreaterThanOrEqual(1)
  })

  it('Tailwind 任意值色彩报错', async () => {
    const msgs = await lintTs(fixture('tailwind-arbitrary.tsx'), 'src/screen/bad.tsx')
    expect(msgs.length).toBeGreaterThanOrEqual(2)
  })

  it('只用语义 token 的写法通过', async () => {
    const msgs = await lintTs(fixture('clean.tsx'), 'src/screen/ok.tsx')
    expect(msgs).toEqual([])
  })

  it('管理界面目录不受约束', async () => {
    const msgs = await lintTs(fixture('color-literal.tsx'), 'src/app/free.tsx')
    expect(msgs).toEqual([])
  })
})

describe('屏幕端与模板目录的样式约束（stylelint）', () => {
  it('CSS 颜色字面量与 --p-* 报错', async () => {
    const warnings = await lintCss(fixture('bad.css'), 'src/templates/bad.css')
    expect(warnings.length).toBeGreaterThanOrEqual(4)
  })

  it('只用语义 token 的 CSS 通过', async () => {
    const warnings = await lintCss(fixture('clean.css'), 'src/templates/ok.css')
    expect(warnings).toEqual([])
  })

  it('管理界面 CSS 不受约束', async () => {
    const warnings = await lintCss(fixture('bad.css'), 'src/app/free.css')
    expect(warnings).toEqual([])
  })
})
