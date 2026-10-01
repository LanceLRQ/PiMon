/// <reference types="node" />
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { getTemplateDef, isImplemented, templateNames } from './registry'
import { WidgetView } from './widget-view'
import { makeWidget, renderIn } from './test-utils'

const read = (rel: string) => readFileSync(new URL(rel, import.meta.url), 'utf8')

describe('模板名集合与 Go 侧对照', () => {
  it('registry 的名字集合等于 manifest.Templates', () => {
    const src = read('../../../src/pkg/plugin/manifest/types.go')
    const block = /var Templates = \[\]string\{([\s\S]*?)\n\}/.exec(src)![1]
    const goNames = [...block.matchAll(/"([\w-]+)"/g)].map((m) => m[1])
    expect(goNames.length).toBeGreaterThanOrEqual(12)
    expect([...templateNames].sort()).toEqual([...goNames].sort())
  })
})

describe('尺寸表与 Go 侧对照', () => {
  const sizeKeys = (name: string) => getTemplateDef(name).sizes.map((s) => `${s.cols}x${s.rows}`)

  it('value、gauge、state 与 catalog.go 的通用目录一致', () => {
    const src = read('../../../src/internal/hub/screens/catalog.go')
    for (const name of ['value', 'gauge', 'state']) {
      const line = new RegExp(`Template: "${name}", Sizes: \\[\\]model\\.WidgetSize\\{([^}]*)\\}`).exec(src)![1]
      const goSizes = [...line.matchAll(/sz\((\d+), (\d+)\)/g)].map((m) => `${m[1]}x${m[2]}`)
      expect(sizeKeys(name)).toEqual(goSizes)
    }
  })

  it('clock、text 与 core/plugin.yaml 一致', () => {
    const yaml = read('../../../src/plugins/core/plugin.yaml')
    for (const name of ['clock', 'text']) {
      const ys = [...yaml.matchAll(new RegExp(`(\\d+x\\d+): \\{template: ${name},`, 'g'))].map((m) => m[1])
      expect(ys.length).toBeGreaterThan(0)
      expect(sizeKeys(name)).toEqual(ys)
    }
  })
})

describe('D5b 模板尺寸表与 Go 侧、各插件 plugin.yaml 对照', () => {
  const sizeKeys = (name: string) => getTemplateDef(name).sizes.map((s) => `${s.cols}x${s.rows}`).sort()
  const catalog = read('../../../src/internal/hub/screens/catalog.go')
  const pluginsDir = join(import.meta.dirname, '../../../src/plugins')
  const manifests = readdirSync(pluginsDir, { withFileTypes: true })
    .filter((e) => e.isDirectory())
    .flatMap((e) => { try { return [readFileSync(join(pluginsDir, e.name, 'plugin.yaml'), 'utf8')] } catch { return [] } })

  // 目录（catalog.go）与各插件 manifest 声明过的尺寸并集
  const declared = (name: string): string[] => {
    const set = new Set<string>()
    const line = new RegExp(`Template: "${name}", Sizes: \\[\\]model\\.WidgetSize\\{([^}]*)\\}`).exec(catalog)?.[1] ?? ''
    for (const m of line.matchAll(/sz\((\d+), (\d+)\)/g)) set.add(`${m[1]}x${m[2]}`)
    const re = new RegExp(`(\\d+x\\d+):\\s*(?:\\{template: ${name},|\\n\\s+template: ${name}\\b)`, 'g')
    for (const src of manifests) for (const m of src.matchAll(re)) set.add(m[1])
    return [...set].sort()
  }

  it.each(['list', 'table', 'status-grid', 'chart', 'weather'])('%s 的尺寸 = 目录与插件声明的并集', (name) => {
    const want = declared(name)
    expect(want.length).toBeGreaterThan(0)
    expect(sizeKeys(name)).toEqual(want)
  })
})

describe('未实现的模板（Ruling 16）', () => {
  afterEach(() => vi.restoreAllMocks())

  it('quota-multi 渲染中性占位且不报 console error', async () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const widget = makeWidget({ template: 'quota-multi', title: 'Codex 额度', size: { cols: 2, rows: 2 } })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    expect(container.textContent).toContain('模板将在后续版本提供')
    expect(container.querySelector('[data-template-pending="quota-multi"]')).not.toBeNull()
    expect(err).not.toHaveBeenCalled()
    expect(warn).not.toHaveBeenCalled()
    expect(isImplemented('quota-multi')).toBe(false)
  })

  it('完全未知的模板名同样占位', async () => {
    const widget = makeWidget({ template: 'nope' })
    const { container } = await renderIn(<WidgetView widget={widget} data={{}} />)
    expect(container.querySelector('[data-template-pending="nope"]')).not.toBeNull()
  })

  it('已实现的模板', () => {
    for (const n of ['value', 'gauge', 'state', 'text', 'clock', 'list', 'table', 'status-grid', 'chart', 'weather']) expect(isImplemented(n)).toBe(true)
  })
})

describe('Ruling 42：承载信息的小字不用 faint 色', () => {
  it('templates 目录的源码不使用 s-faint 与 --foreground-faint', () => {
    const files = import.meta.glob('./*.{ts,tsx}', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
    const names = Object.keys(files).filter((f) => !/\.test\./.test(f))
    expect(names.length).toBeGreaterThan(10)
    for (const f of names) expect(files[f], f).not.toMatch(/s-faint|foreground-faint/)
  })
})
