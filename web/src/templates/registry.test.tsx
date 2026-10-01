/// <reference types="node" />
import { readFileSync } from 'node:fs'
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

  it('本子任务的五个模板已实现', () => {
    for (const n of ['value', 'gauge', 'state', 'text', 'clock']) expect(isImplemented(n)).toBe(true)
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
