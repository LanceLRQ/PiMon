import { act, render, waitFor } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { describe, expect, it, vi } from 'vitest'
import { createI18n } from '@/i18n'
import type { ThemeId, ThemeRuntime } from '@/themes'
import { EditorCanvas, type EditorCanvasProps } from './EditorCanvas'

const baseProps = (themeId: ThemeId, onThemeRuntime: (rt: ThemeRuntime) => void): EditorCanvasProps => ({
  screen: { id: 'index', name: '首页', dwell_seconds: 0, in_rotation: true, widgets: [] },
  grid: { cols: 8, rows: 5 },
  data: {},
  themeId,
  reduceEffects: false,
  viewport: { w: 1024, h: 600 },
  selectedId: null,
  conflictIds: [],
  dragEntry: null,
  lang: 'zh',
  timezone: 'UTC',
  now: () => 0,
  onSelect() {},
  onMove() {},
  onDropEntry() {},
  onThemeRuntime,
})

async function setup() {
  const i18n = await createI18n('zh')
  const seen: ThemeRuntime[] = []
  const onRuntime = vi.fn((rt: ThemeRuntime) => seen.push(rt))
  const view = (key: string, themeId: ThemeId) => (
    <I18nextProvider i18n={i18n}>
      <EditorCanvas key={key} {...baseProps(themeId, onRuntime)} />
    </I18nextProvider>
  )
  return { view, seen, onRuntime }
}

const host = (root: HTMLElement) => root.querySelector('[data-theme]') as HTMLElement

describe('编辑器画布的主题监听', () => {
  it('主题 token 通过画布容器上的 data-theme 生效', async () => {
    const { view, seen } = await setup()
    const { container } = render(view('a', 'mission-control'))
    expect(host(container)).toHaveAttribute('data-theme', 'mission-control')
    expect(seen.at(-1)?.themeId).toBe('mission-control')
  })

  it('切换预览主题后读数更新，之后容器上的属性变化仍会被监听到', async () => {
    const { view, seen } = await setup()
    const { container, rerender } = render(view('a', 'ambient'))
    rerender(view('a', 'industrial'))
    expect(seen.at(-1)?.themeId).toBe('industrial')
    act(() => host(container).setAttribute('data-theme', 'mission-control'))
    await waitFor(() => expect(seen.at(-1)?.themeId).toBe('mission-control'))
  })

  it('画布容器重挂载后对新容器重新 watch，旧容器不再触发', async () => {
    const { view, seen, onRuntime } = await setup()
    const { container, rerender } = render(view('a', 'ambient'))
    const oldHost = host(container)
    rerender(view('b', 'ambient'))
    const newHost = host(container)
    expect(newHost).not.toBe(oldHost)
    onRuntime.mockClear()
    act(() => newHost.setAttribute('data-theme', 'industrial'))
    await waitFor(() => expect(seen.at(-1)?.themeId).toBe('industrial'))
    onRuntime.mockClear()
    act(() => oldHost.setAttribute('data-theme', 'mission-control'))
    await new Promise((r) => setTimeout(r, 20))
    expect(onRuntime).not.toHaveBeenCalled()
  })
})
