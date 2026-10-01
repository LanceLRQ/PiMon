import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiError, defaultPlugins, json, mockApi, renderWithApp, seedStore, type ApiHandler } from '@/pages/instances/test-utils'
import { makeInstance } from '@/pages/instances/instances.fixtures'
import { liveStore } from '@/store/live-store'
import type { Layout, LayoutState, LayoutVersionInfo, LayoutWidget, PluginInfo, ScreenStatus, WidgetCatalog } from '@/types/generated'
import { LayoutEditor } from './LayoutEditor'

// jsdom 没有 DragEvent：补一个继承 MouseEvent 的，让 dragover/drop 带上坐标
beforeAll(() => {
  if (!('DragEvent' in window)) {
    Object.defineProperty(window, 'DragEvent', { value: class DragEventPolyfill extends MouseEvent {}, configurable: true })
  }
})

const sz = (cols: number, rows: number) => ({ cols, rows })
const bind = (slot: string, item: string) => ({ slot, list: false, refs: [{ item }] })

const demo: PluginInfo = {
  ...defaultPlugins.plugins[0],
  id: 'demo',
  name: '演示数据',
  widgets: [
    { id: 'cpu', name: 'CPU', sizes: [
      { size: '1x1', cols: 1, rows: 1, template: 'gauge', bind: [bind('value', 'cpu.pi')] },
      { size: '2x1', cols: 2, rows: 1, template: 'value', bind: [bind('value', 'cpu.pi')] },
      { size: '2x2', cols: 2, rows: 2, template: 'value', bind: [bind('value', 'cpu.pi')] },
    ] },
  ],
}
const core: PluginInfo = {
  ...demo,
  id: 'core',
  name: '系统',
  outputs: [],
  widgets: [{ id: 'text', name: '文本', sizes: [{ size: '2x1', cols: 2, rows: 1, template: 'text', bind: [] }] }],
}
const catalog: WidgetCatalog = {
  generic: [{ template: 'value', sizes: [sz(1, 1), sz(2, 1)] }, { template: 'gauge', sizes: [sz(1, 1)] }],
  aggregate: [{ template: 'status-grid', sizes: [sz(2, 2), sz(4, 2)] }],
}

const pw = (id: string, col: number, row: number, cols: number, rows: number, over: Partial<LayoutWidget> = {}): LayoutWidget => ({
  id, source: 'plugin', plugin_id: 'demo', widget_id: 'cpu', size: sz(cols, rows), col, row,
  binding: { instance_id: 'i1' }, options: {}, ...over,
})
const layout = (): Layout => ({
  grid: { cols: 8, rows: 5 },
  screens: [
    { id: 'index', name: '首页', dwell_seconds: 0, in_rotation: true, widgets: [pw('a', 0, 0, 2, 2), pw('b', 3, 0, 1, 1), pw('far', 7, 4, 1, 1)] },
    { id: 's1', name: '主机', dwell_seconds: 0, in_rotation: true, widgets: [] },
  ],
})
const layoutState = (): LayoutState => ({ version: 3, source: 'edit', created_at: '2026-10-01T00:00:00Z', layout: layout(), broken: [] })
const status: ScreenStatus = {
  state: { mode: 'on', theme_id: 'industrial', reason: 'schedule' },
  viewport: { w: 1024, h: 600, dpr: 1 },
  online: true,
}

function handler(over: ApiHandler = () => undefined): ApiHandler {
  return (req) => {
    const custom = over(req)
    if (custom) return custom
    const path = req.url.split('?')[0]
    if (path === '/api/screens') return json(200, layoutState())
    if (path === '/api/screens/catalog') return json(200, catalog)
    if (path === '/api/screen/status') return json(200, status)
    if (path === '/api/plugins') return json(200, { ...defaultPlugins, plugins: [demo, core] })
    if (path.startsWith('/api/instances/')) return apiError(404, 'not_found')
    return undefined
  }
}

async function mount(over?: ApiHandler) {
  const api = mockApi(handler(over))
  seedStore([makeInstance({ id: 'i1', name: '树莓派 CPU', plugin_id: 'demo' }), makeInstance({ id: 'i2', name: '第二个', plugin_id: 'demo', created_at: '2027-01-01T00:00:00Z' })])
  await renderWithApp(<LayoutEditor />)
  await screen.findByTestId('layout-editor')
  return api
}

const widgetEl = (id: string) => screen.getByTestId(`editor-widget-${id}`)
const label = (id: string) => widgetEl(id).getAttribute('aria-label') ?? ''

beforeEach(() => {
  liveStore.reset()
  localStorage.clear()
})
afterEach(() => {
  vi.unstubAllGlobals()
})

describe('布局编辑器', () => {
  it('三栏载入：库三个 Tab、画布小组件、检查器概要、基于的版本号', async () => {
    await mount()
    expect(screen.getByTestId('base-version')).toHaveTextContent('基于 v3')
    expect(screen.getAllByRole('tab', { name: /插件声明|通用|聚合/ })).toHaveLength(3)
    expect(screen.getByRole('tab', { name: /插件声明/ })).toHaveTextContent('2')
    expect(widgetEl('a')).toBeInTheDocument()
    expect(screen.getByTestId('inspector')).toHaveTextContent('概要')
    expect(screen.getByTestId('viewport-info')).toHaveTextContent('1024×600 · 在线')
  })

  it('库可切换 Tab 并按名称或插件 id 搜索', async () => {
    const user = userEvent.setup()
    await mount()
    expect(screen.getByTestId('lib-item-plugin:demo:cpu')).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: /通用/ }))
    expect(screen.getByTestId('lib-item-generic:value')).toBeInTheDocument()
    expect(screen.queryByTestId('lib-item-plugin:demo:cpu')).toBeNull()
    await user.click(screen.getByRole('tab', { name: /插件声明/ }))
    await user.type(screen.getByRole('searchbox'), 'core')
    expect(screen.getByTestId('lib-item-plugin:core:text')).toBeInTheDocument()
    expect(screen.queryByTestId('lib-item-plugin:demo:cpu')).toBeNull()
    await user.clear(screen.getByRole('searchbox'))
    await user.type(screen.getByRole('searchbox'), '没有这个')
    expect(screen.getByText('没有匹配的小组件')).toBeInTheDocument()
  })

  it('点选小组件：检查器显示它，标尺高亮占用的列与行，Esc 取消选中', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(widgetEl('a'))
    expect(widgetEl('a')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('inspector')).toHaveAttribute('data-widget-id', 'a')
    const hlX = Array.from(document.querySelectorAll('[data-ruler="x"][data-highlight]')).map((e) => e.textContent)
    const hlY = Array.from(document.querySelectorAll('[data-ruler="y"][data-highlight]')).map((e) => e.textContent)
    expect(hlX).toEqual(['1', '2'])
    expect(hlY).toEqual(['1', '2'])
    fireEvent.keyDown(widgetEl('a'), { key: 'Escape' })
    expect(widgetEl('a')).toHaveAttribute('aria-pressed', 'false')
    expect(document.querySelectorAll('[data-ruler][data-highlight]')).toHaveLength(0)
  })

  it('方向键按格移动，撞到别的小组件或边界时不动并提示、冲突块高亮', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(widgetEl('b'))
    fireEvent.keyDown(widgetEl('b'), { key: 'ArrowRight' })
    expect(label('b')).toContain('第 5 列第 1 行')
    fireEvent.keyDown(widgetEl('b'), { key: 'ArrowDown' })
    expect(label('b')).toContain('第 5 列第 2 行')
    // 把 b 挪到 a 右侧再往左撞
    await user.click(widgetEl('b'))
    for (const k of ['ArrowUp', 'ArrowLeft', 'ArrowLeft']) fireEvent.keyDown(widgetEl('b'), { key: k })
    expect(label('b')).toContain('第 3 列第 1 行')
    fireEvent.keyDown(widgetEl('b'), { key: 'ArrowLeft' })
    expect(label('b')).toContain('第 3 列第 1 行')
    expect(await screen.findByText('放不下：与其他小组件重叠')).toBeInTheDocument()
    expect(widgetEl('a')).toHaveAttribute('data-conflict', 'true')
    // 到网格边缘
    await user.click(widgetEl('far'))
    fireEvent.keyDown(widgetEl('far'), { key: 'ArrowRight' })
    expect(label('far')).toContain('第 8 列第 5 行')
    expect(await screen.findByText('放不下：超出网格')).toBeInTheDocument()
  })

  it('Delete 删除选中小组件；输入框里的按键不触发画布操作', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(widgetEl('b'))
    const title = screen.getByLabelText('标题')
    fireEvent.keyDown(title, { key: 'Delete' })
    expect(screen.queryByTestId('editor-widget-b')).toBeInTheDocument()
    fireEvent.keyDown(widgetEl('b'), { key: 'Delete' })
    expect(screen.queryByTestId('editor-widget-b')).toBeNull()
    expect(screen.getByTestId('inspector')).toHaveTextContent('概要')
  })

  it('点「添加」放到第一个放得下的空位并选中', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(screen.getByRole('button', { name: '添加「CPU」到画布' }))
    const added = document.querySelector('[data-selected="true"]') as HTMLElement
    expect(added).not.toBeNull()
    expect(added.getAttribute('aria-label')).toContain('第 3 列第 1 行')
    expect(added.getAttribute('aria-label')).toContain('1×1')
    expect(screen.getByTestId('inspector')).toHaveTextContent('demo · gauge')
  })

  describe('拖放', () => {
    const dataTransfer = { setData() {}, effectAllowed: 'none' }
    const point = (col: number, row: number) => ({ clientX: col * 128 + 64, clientY: row * 120 + 60 })

    it('从库拖入：落在空格吸附放置，落在占用格显示红色幽灵块且松手不添加', async () => {
      await mount()
      const bezel = screen.getByTestId('editor-bezel')
      bezel.getBoundingClientRect = () => ({ left: 0, top: 0, width: 1024, height: 600, right: 1024, bottom: 600, x: 0, y: 0, toJSON() {} })
      const item = screen.getByTestId('lib-item-plugin:demo:cpu')
      fireEvent.dragStart(item, { dataTransfer })
      // 占用格：a 在 (0,0)-(1,1)
      fireEvent.dragOver(bezel, point(0, 0))
      expect(screen.getByTestId('editor-ghost')).toHaveAttribute('data-ok', 'false')
      expect(screen.getByTestId('editor-ghost')).toHaveTextContent('放不下')
      fireEvent.drop(bezel, point(0, 0))
      expect(document.querySelectorAll('[data-testid^="editor-widget-"]')).toHaveLength(3)
      expect(await screen.findByText('放不下：与其他小组件重叠')).toBeInTheDocument()
      // 空格：第 6 列第 3 行
      fireEvent.dragStart(item, { dataTransfer })
      fireEvent.dragOver(bezel, point(5, 2))
      expect(screen.getByTestId('editor-ghost')).toHaveAttribute('data-ok', 'true')
      fireEvent.drop(bezel, point(5, 2))
      const added = document.querySelector('[data-selected="true"]') as HTMLElement
      expect(added.getAttribute('aria-label')).toContain('第 6 列第 3 行')
      expect(screen.queryByTestId('editor-ghost')).toBeNull()
    })

    it('拖动已有小组件：按格吸附，松手落在空格；落在占用格则回到原位', async () => {
      await mount()
      const bezel = screen.getByTestId('editor-bezel')
      bezel.getBoundingClientRect = () => ({ left: 0, top: 0, width: 1024, height: 600, right: 1024, bottom: 600, x: 0, y: 0, toJSON() {} })
      const b = widgetEl('b')
      // b 在第 4 列第 1 行（col 3,row 0），抓取点取其中心
      fireEvent.pointerDown(b, { button: 0, pointerId: 1, ...point(3, 0) })
      fireEvent.pointerMove(b, { pointerId: 1, ...point(5, 3) })
      expect(screen.getByTestId('editor-ghost')).toHaveAttribute('data-ok', 'true')
      fireEvent.pointerUp(b, { pointerId: 1, ...point(5, 3) })
      expect(label('b')).toContain('第 6 列第 4 行')
      // 再拖到 a 上
      fireEvent.pointerDown(widgetEl('b'), { button: 0, pointerId: 2, ...point(5, 3) })
      fireEvent.pointerMove(widgetEl('b'), { pointerId: 2, ...point(1, 1) })
      expect(screen.getByTestId('editor-ghost')).toHaveAttribute('data-ok', 'false')
      fireEvent.pointerUp(widgetEl('b'), { pointerId: 2, ...point(1, 1) })
      expect(label('b')).toContain('第 6 列第 4 行')
      expect(await screen.findByText('放不下：与其他小组件重叠')).toBeInTheDocument()
    })
  })

  describe('检查器', () => {
    it('尺寸只能在声明的尺寸之间切换，撞车时拒绝', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(widgetEl('b'))
      const seg = screen.getByRole('radiogroup', { name: '尺寸' })
      expect(within(seg).getAllByRole('radio').map((r) => r.textContent)).toEqual(['1×1', '2×1', '2×2'])
      await user.click(within(seg).getByRole('radio', { name: '2×1' }))
      expect(label('b')).toContain('2×1')
      // 2x2 会越出 b 所在列右侧？b 在 col3：3,4 / rows 0,1 无冲突
      await user.click(within(seg).getByRole('radio', { name: '2×2' }))
      expect(label('b')).toContain('2×2')
      // a 选中后 2x2 已是当前尺寸；把 a 放大受限于 b：a 在 0..1，b 在 3..4，通用目录不会给 a 更大尺寸
      await user.click(widgetEl('far'))
      await user.click(within(screen.getByRole('radiogroup', { name: '尺寸' })).getByRole('radio', { name: '2×1' }))
      expect(await screen.findByText('放不下：超出网格')).toBeInTheDocument()
      expect(label('far')).toContain('1×1')
    })

    it('位置步进器改列与行并受碰撞保护', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(widgetEl('b'))
      await user.click(screen.getByRole('button', { name: '右移' }))
      expect(label('b')).toContain('第 5 列第 1 行')
      await user.click(screen.getByRole('button', { name: '下移' }))
      expect(label('b')).toContain('第 5 列第 2 行')
    })

    it('清空标题时删除 title 键，画布回到默认标题（不写 null）', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(widgetEl('b'))
      const input = screen.getByLabelText('标题')
      await user.type(input, '我的 CPU')
      expect(label('b')).toContain('我的 CPU')
      await user.clear(input)
      expect(label('b')).toContain('CPU，第')
      expect(input).toHaveValue('')
    })

    it('手动阈值只对数值模板出现；开启后可输入警告与严重阈值和方向', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(widgetEl('b'))
      expect(screen.queryByRole('switch', { name: '手动阈值配色' })).not.toBeNull()
      await user.click(screen.getByRole('switch', { name: '手动阈值配色' }))
      await user.type(screen.getByLabelText('警告阈值'), '70')
      await user.type(screen.getByLabelText('严重阈值'), '90')
      await user.click(screen.getByRole('radio', { name: '值达到或低于阈值时升级' }))
      expect(screen.getByLabelText('警告阈值')).toHaveValue('70')
      expect(screen.getByRole('radio', { name: '值达到或低于阈值时升级' })).toHaveAttribute('aria-checked', 'true')
    })

    it('绑定实例下拉只列出该插件的实例，可切换为自动', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(widgetEl('b'))
      const sel = screen.getByTestId('insp-instance')
      expect(within(sel).getAllByRole('option').map((o) => o.textContent)).toEqual(['自动（该插件最早的实例）', '树莓派 CPU', '第二个'])
      await user.selectOptions(sel, 'i2')
      expect(sel).toHaveValue('i2')
    })
  })

  describe('网格', () => {
    it('缩小到会让小组件越界的网格时弹越界对话框；取消保持原网格，确认则移除越界者并缩小', async () => {
      const user = userEvent.setup()
      await mount()
      const sel = screen.getByRole('combobox', { name: '网格' })
      expect(within(sel).getByRole('option', { name: /6×4 · 1 个越界/ })).toBeInTheDocument()
      await user.selectOptions(sel, '6x4')
      const dlg = await screen.findByRole('dialog')
      expect(dlg).toHaveTextContent('缩小到 6×4 会让 1 个小组件越界')
      expect(within(dlg).getByTestId('oob-list')).toHaveTextContent('index 首页')
      expect(within(dlg).getByTestId('oob-list')).toHaveTextContent('c8 r5 · 1×1')
      await user.click(within(dlg).getByRole('button', { name: '保持现有网格' }))
      expect(sel).toHaveValue('8x5')
      expect(widgetEl('far')).toBeInTheDocument()
      expect(screen.getByTestId('dirty-chip')).toHaveAttribute('data-dirty', 'false')

      await user.selectOptions(sel, '6x4')
      await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: /移除 1 个并缩小/ }))
      expect(sel).toHaveValue('6x4')
      expect(screen.queryByTestId('editor-widget-far')).toBeNull()
      expect(within(screen.getByTestId('changes-panel')).getAllByRole('listitem')).toHaveLength(2)
    })

    it('放大网格直接生效，标尺随之增加', async () => {
      const user = userEvent.setup()
      await mount()
      await user.selectOptions(screen.getByRole('combobox', { name: '网格' }), '10x6')
      expect(document.querySelectorAll('[data-ruler="x"]')).toHaveLength(10)
      expect(document.querySelectorAll('[data-ruler="y"]')).toHaveLength(6)
    })
  })

  describe('主题', () => {
    const themeHost = () => document.querySelector('[data-theme]:not(html)[data-reduce-effects], [data-testid="editor-bezel"] [data-theme]') as HTMLElement

    it('画布容器上带当前屏幕主题；切换预览主题后重新 watch，运行时读数跟着变', async () => {
      const user = userEvent.setup()
      await mount()
      expect(themeHost()).toHaveAttribute('data-theme', 'industrial')
      await waitFor(() => expect(screen.getByTestId('stage-info')).toHaveTextContent(/^industrial/))
      await user.selectOptions(screen.getByRole('combobox', { name: '预览主题' }), 'ambient')
      expect(themeHost()).toHaveAttribute('data-theme', 'ambient')
      await waitFor(() => expect(screen.getByTestId('stage-info')).toHaveTextContent(/^ambient/))
      // 切换之后新的 watch 仍然生效：外部直接改属性也能被读到
      act(() => themeHost().setAttribute('data-theme', 'mission-control'))
      await waitFor(() => expect(screen.getByTestId('stage-info')).toHaveTextContent(/^mission-control/))
    })
  })

  describe('预览状态', () => {
    const frame = (id: string) => document.querySelector(`[data-widget-frame][data-widget-id="${id}"]`) as HTMLElement

    it('切换后画布所有小组件按该级别显示，切回实际恢复；草稿不变', async () => {
      const user = userEvent.setup()
      await mount()
      const real = frame('a').getAttribute('data-status-level')
      expect(real).not.toBe('critical')
      await user.click(widgetEl('b'))
      const switchBefore = screen.getByRole('switch', { name: '手动阈值配色' }).getAttribute('aria-checked')
      const labelsBefore = ['a', 'b', 'far'].map(label)
      const seg = screen.getByRole('radiogroup', { name: '预览状态' })
      await user.click(within(seg).getByRole('radio', { name: '全部按 critical 显示（仅画布）' }))
      for (const id of ['a', 'b', 'far']) expect(frame(id)).toHaveAttribute('data-status-level', 'critical')
      // 草稿：检查器读到的手动阈值开关、位置与尺寸都没被预览改动
      expect(screen.getByRole('switch', { name: '手动阈值配色' }).getAttribute('aria-checked')).toBe(switchBefore)
      expect(['a', 'b', 'far'].map(label)).toEqual(labelsBefore)
      await user.click(within(seg).getByRole('radio', { name: '全部按 ok 显示（仅画布）' }))
      expect(frame('a')).toHaveAttribute('data-status-level', 'ok')
      await user.click(within(seg).getByRole('radio', { name: '按真实数据显示' }))
      expect(frame('a').getAttribute('data-status-level')).toBe(real)
      expect(screen.getByRole('switch', { name: '手动阈值配色' }).getAttribute('aria-checked')).toBe(switchBefore)
    })
  })

  describe('编辑器级快捷键与检查器重置', () => {
    it('焦点不在画布时 Esc 与方向键仍生效，输入控件内不生效', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(widgetEl('b'))
      fireEvent.keyDown(document.body, { key: 'ArrowRight' })
      expect(label('b')).toContain('第 5 列第 1 行')
      fireEvent.keyDown(screen.getByRole('searchbox'), { key: 'ArrowRight' })
      expect(label('b')).toContain('第 5 列第 1 行')
      fireEvent.keyDown(document.body, { key: 'Escape' })
      expect(widgetEl('b')).toHaveAttribute('aria-pressed', 'false')
    })

    it('切换选中的小组件时检查器内部状态重置', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(widgetEl('a'))
      await user.click(screen.getByRole('switch', { name: '手动阈值配色' }))
      await user.type(screen.getByLabelText('警告阈值'), '55')
      await user.click(widgetEl('b'))
      expect(screen.getByRole('switch', { name: '手动阈值配色' })).toHaveAttribute('aria-checked', 'false')
      await user.click(widgetEl('a'))
      expect(screen.getByLabelText('警告阈值')).toHaveValue('55')
    })

    it('拖动松手按 pointerup 的坐标落点，而不是上一帧的幽灵块', async () => {
      await mount()
      const bezel = screen.getByTestId('editor-bezel')
      bezel.getBoundingClientRect = () => ({ left: 0, top: 0, width: 1024, height: 600, right: 1024, bottom: 600, x: 0, y: 0, toJSON() {} })
      const b = widgetEl('b')
      fireEvent.pointerDown(b, { button: 0, pointerId: 1, clientX: 3 * 128 + 64, clientY: 60 })
      fireEvent.pointerMove(b, { pointerId: 1, clientX: 4 * 128 + 64, clientY: 60 })
      fireEvent.pointerUp(b, { pointerId: 1, clientX: 6 * 128 + 64, clientY: 2 * 120 + 60 })
      expect(label('b')).toContain('第 7 列第 3 行')
    })
  })

  it('载入失败显示错误并可重试', async () => {
    const user = userEvent.setup()
    let fail = true
    await (async () => {
      mockApi(handler((req) => (fail && req.url.startsWith('/api/screens') ? apiError(500, 'internal') : undefined)))
      seedStore([])
      await renderWithApp(<LayoutEditor />)
    })()
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    fail = false
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByTestId('layout-editor')).toBeInTheDocument()
  })

  it('手机只显示「请用电脑编辑布局」', async () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: true, media: q, addEventListener() {}, removeEventListener() {} }))
    mockApi(handler())
    await renderWithApp(<LayoutEditor />)
    expect(screen.getByText('请用电脑编辑布局')).toBeInTheDocument()
    expect(screen.queryByTestId('layout-editor')).toBeNull()
    expect(screen.queryByTestId('editor-canvas-area')).toBeNull()
  })
})

describe('未保存改动、保存与版本历史', () => {
  const changeRows = () => within(screen.getByTestId('changes-panel')).queryAllByRole('listitem')
  const moveB = (key: string) => fireEvent.keyDown(document.body, { key })

  it('焦点在检查器按钮上时 Delete、Backspace 与方向键不作用于选中的小组件', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(widgetEl('b'))
    const right = screen.getByRole('button', { name: '右移' })
    right.focus()
    for (const key of ['Delete', 'Backspace', 'ArrowRight']) fireEvent.keyDown(right, { key })
    expect(widgetEl('b')).toBeInTheDocument()
    expect(label('b')).toContain('第 4 列第 1 行')
    expect(screen.getByTestId('dirty-chip')).toHaveAttribute('data-dirty', 'false')
  })

  it('改动进入清单；单条撤销让画布位置真正回退，清单撤空后显示已与 v3 一致', async () => {
    const user = userEvent.setup()
    await mount()
    expect(screen.getByTestId('dirty-chip')).toHaveTextContent('已与 v3 一致')
    await user.click(widgetEl('b'))
    moveB('ArrowRight')
    moveB('ArrowDown')
    expect(label('b')).toContain('第 5 列第 2 行')
    expect(screen.getByTestId('dirty-chip')).toHaveTextContent('未保存 1 处')
    expect(changeRows()).toHaveLength(1)
    expect(changeRows()[0]).toHaveTextContent('移动')
    await user.click(within(changeRows()[0]).getByRole('button', { name: '撤销此条' }))
    expect(label('b')).toContain('第 4 列第 1 行')
    expect(changeRows()).toHaveLength(0)
    expect(screen.getByTestId('dirty-chip')).toHaveTextContent('已与 v3 一致')
    expect(screen.getByTestId('changes-panel')).toHaveTextContent('没有未保存的改动')
  })

  it('顶栏撤销只撤最近一条', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(widgetEl('b'))
    moveB('ArrowRight')
    await user.click(widgetEl('a'))
    moveB('ArrowRight')
    expect(changeRows()).toHaveLength(2)
    await user.click(screen.getByRole('button', { name: /撤销$/ }))
    expect(label('a')).toContain('第 1 列第 1 行')
    expect(label('b')).toContain('第 5 列第 1 行')
    expect(changeRows()).toHaveLength(1)
  })

  it('单条撤销移动被新增的小组件占位时：拒绝、高亮冲突块、位置不变、有说明', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(widgetEl('a'))
    moveB('ArrowRight')
    await user.click(screen.getByRole('button', { name: '添加「CPU」到画布' }))
    const moveRow = changeRows().find((r) => r.textContent?.includes('移动'))!
    await user.click(within(moveRow).getByRole('button', { name: '撤销此条' }))
    expect(await screen.findByText(/无法撤销：恢复后会与当前草稿重叠/)).toBeInTheDocument()
    expect(label('a')).toContain('第 2 列第 1 行')
    expect(document.querySelectorAll('[data-conflict="true"]')).toHaveLength(1)
    expect(changeRows()).toHaveLength(2)
  })

  it('放弃：确认后草稿回到基线', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(widgetEl('b'))
    moveB('ArrowRight')
    await user.click(screen.getByRole('button', { name: '放弃' }))
    expect(await screen.findByRole('dialog')).toHaveTextContent('草稿会恢复为 v3，1 处改动将丢失')
    await user.click(screen.getByRole('button', { name: '放弃改动' }))
    expect(label('b')).toContain('第 4 列第 1 行')
    expect(screen.getByTestId('dirty-chip')).toHaveAttribute('data-dirty', 'false')
  })

  it('有未保存改动时关闭标签页会触发浏览器确认；无改动时不会', async () => {
    const user = userEvent.setup()
    await mount()
    const fire = () => {
      const e = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(e)
      return e.defaultPrevented
    }
    expect(fire()).toBe(false)
    await user.click(widgetEl('b'))
    moveB('ArrowRight')
    expect(fire()).toBe(true)
  })

  describe('保存', () => {
    const saved = (version: number, l: Layout = layout()): LayoutState => ({ version, source: 'edit', created_at: '2026-10-01T00:00:00Z', layout: l, broken: [] })

    it('保存带 base_version 与整份草稿，成功后基线更新、清单清空', async () => {
      const user = userEvent.setup()
      const api = await mount((req) => (req.method === 'PUT' && req.url === '/api/screens' ? json(200, saved(4, (req.body as { layout: Layout }).layout)) : undefined))
      expect(screen.getByRole('button', { name: '保存并推送' })).toBeDisabled()
      await user.click(widgetEl('b'))
      moveB('ArrowRight')
      await user.click(screen.getByRole('button', { name: '保存并推送' }))
      await waitFor(() => expect(screen.getByTestId('base-version')).toHaveTextContent('基于 v4'))
      const put = api.calls.find((c) => c.method === 'PUT')!
      expect(put.body).toMatchObject({ base_version: 3 })
      const b = (put.body as { layout: Layout }).layout.screens[0].widgets.find((w) => w.id === 'b')
      expect(b).toMatchObject({ col: 4, row: 0 })
      expect(screen.getByTestId('dirty-chip')).toHaveTextContent('已与 v4 一致')
      expect(label('b')).toContain('第 5 列第 1 行')
    })

    it('版本冲突：提示并可查看对方改动，再以我的为准基于最新版本重新提交', async () => {
      const user = userEvent.setup()
      const theirs = layout()
      theirs.screens[0].widgets = theirs.screens[0].widgets.map((w) => (w.id === 'a' ? { ...w, col: 5 } : w))
      let puts = 0
      const api = await mount((req) => {
        if (req.method === 'PUT') {
          puts++
          return puts === 1 ? apiError(409, 'layout.conflict', { latest_version: 5 }) : json(200, saved(6, (req.body as { layout: Layout }).layout))
        }
        if (req.method === 'GET' && req.url === '/api/screens' && puts > 0) return json(200, saved(5, theirs))
        return undefined
      })
      await user.click(widgetEl('b'))
      moveB('ArrowRight')
      await user.click(screen.getByRole('button', { name: '保存并推送' }))
      const dlg = await screen.findByRole('dialog')
      expect(dlg).toHaveTextContent('服务端当前已是 v5')
      await user.click(within(dlg).getByRole('button', { name: '查看对方改动' }))
      const list = await within(dlg).findByTestId('conflict-theirs')
      expect(list).toHaveTextContent('移动')
      expect(list).toHaveTextContent('c1 r1')
      expect(list).toHaveTextContent('c6 r1')
      await user.click(within(dlg).getByRole('button', { name: '以我的为准，基于最新版本重新提交' }))
      await waitFor(() => expect(screen.getByTestId('base-version')).toHaveTextContent('基于 v6'))
      const puts2 = api.calls.filter((c) => c.method === 'PUT')
      expect(puts2[1].body).toMatchObject({ base_version: 5 })
      const a = (puts2[1].body as { layout: Layout }).layout.screens[0].widgets.find((w) => w.id === 'a')
      expect(a).toMatchObject({ col: 0 })
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    it('布局校验失败：toast 提示问题数，草稿保留', async () => {
      const user = userEvent.setup()
      await mount((req) => (req.method === 'PUT' ? apiError(400, 'layout.invalid', { problems: [{ code: 'overlap' }, { code: 'overlap' }] }) : undefined))
      await user.click(widgetEl('b'))
      moveB('ArrowRight')
      await user.click(screen.getByRole('button', { name: '保存并推送' }))
      expect(await screen.findByText(/布局未通过校验（2 处问题）/)).toBeInTheDocument()
      expect(screen.getByTestId('dirty-chip')).toHaveAttribute('data-dirty', 'true')
    })
  })

  describe('版本历史', () => {
    const info = (version: number, over: Partial<LayoutVersionInfo> = {}): LayoutVersionInfo => ({
      version, source: 'edit', created_at: '2026-10-01T08:00:00Z',
      summary: { changed_screens: ['index'], widgets_added: 1, widgets_removed: 0, widgets_changed: 0, grid_changed: false, reordered: false }, has_broken: false, ...over,
    })
    const versions = [info(3), info(2, { has_broken: true }), info(1, { source: 'seed' })]
    const old = (): LayoutState => {
      const l = layout()
      l.screens[0].widgets = l.screens[0].widgets.filter((w) => w.id !== 'far')
      return { version: 2, source: 'edit', created_at: '2026-09-30T00:00:00Z', layout: l, broken: [{ screen: 'index', widget: 'b', code: 'instance_missing' }] }
    }
    const histHandler = (req: { method: string; url: string }) => {
      if (req.url === '/api/screens/versions') return json(200, versions)
      if (req.url === '/api/screens/versions/2') return json(200, old())
      if (req.method === 'POST' && req.url === '/api/screens/rollback') return json(200, { ...old(), version: 4, source: 'rollback' })
      return undefined
    }

    it('抽屉列出版本，标出当前与含失效引用的版本；预览显示缩略图、差异与失效提示', async () => {
      const user = userEvent.setup()
      await mount(histHandler)
      await user.click(screen.getByRole('button', { name: '版本历史' }))
      const list = await screen.findByTestId('history-list')
      expect(within(list).getAllByRole('listitem')).toHaveLength(3)
      expect(within(list).getByText('当前')).toBeInTheDocument()
      expect(list.querySelector('[data-version="2"]')).toHaveTextContent('含失效引用')
      await user.click(within(list.querySelector('[data-version="2"]') as HTMLElement).getByRole('button', { name: '预览' }))
      expect(await screen.findByTestId('mini-map')).toBeInTheDocument()
      expect(screen.getByTestId('history-broken')).toHaveTextContent('绑定的实例已不存在')
      expect(screen.getByTestId('history-diff')).toHaveTextContent('删除')
    })

    it('回滚前提示失效引用，确认后调用接口并把基线换成新版本', async () => {
      const user = userEvent.setup()
      const api = await mount(histHandler)
      await user.click(screen.getByRole('button', { name: '版本历史' }))
      const list = await screen.findByTestId('history-list')
      await user.click(within(list.querySelector('[data-version="2"]') as HTMLElement).getByRole('button', { name: '预览' }))
      await screen.findByTestId('mini-map')
      await user.click(screen.getByRole('button', { name: '回滚到此版本' }))
      const confirm = (await screen.findAllByRole('dialog')).find((d) => d.textContent?.includes('回滚到 v2？'))!
      expect(within(confirm).getByTestId('rollback-broken')).toHaveTextContent('绑定的实例已不存在')
      expect(confirm).toHaveTextContent('会生成新版本 v4')
      await user.click(within(confirm).getByRole('button', { name: '回滚到 v2' }))
      await waitFor(() => expect(screen.getByTestId('base-version')).toHaveTextContent('基于 v4'))
      expect(api.calls.find((c) => c.method === 'POST')?.body).toEqual({ version: 2 })
      expect(screen.queryByTestId('editor-widget-far')).toBeNull()
    })

    it('当前版本不能回滚；有未保存改动时确认框提示会丢失', async () => {
      const user = userEvent.setup()
      await mount(histHandler)
      await user.click(widgetEl('b'))
      moveB('ArrowRight')
      await user.click(screen.getByRole('button', { name: '版本历史' }))
      const list = await screen.findByTestId('history-list')
      await user.click(within(list.querySelector('[data-version="2"]') as HTMLElement).getByRole('button', { name: '预览' }))
      await screen.findByTestId('mini-map')
      await user.click(screen.getByRole('button', { name: '回滚到此版本' }))
      expect(await screen.findByText(/你有 1 处未保存的改动，回滚后会丢失/)).toBeInTheDocument()
    })
  })

  describe('保存期间锁定、模态下的快捷键、screen 标签', () => {
    it('保存在途时画布、检查器、库、快捷键与按钮都不响应，成功后以服务端结果为准且不丢失已发出的改动', async () => {
      const user = userEvent.setup()
      let release: (r: Response) => void = () => {}
      const api = await mount((req) => (req.method === 'PUT' ? new Promise<Response>((res) => { release = res }) as unknown as Response : undefined))
      await user.click(widgetEl('b'))
      fireEvent.keyDown(document.body, { key: 'ArrowRight' })
      await user.click(screen.getByRole('button', { name: '保存并推送' }))
      expect(await screen.findByRole('button', { name: '正在保存…' })).toBeDisabled()
      // 在途时的编辑一律被忽略
      fireEvent.keyDown(document.body, { key: 'ArrowRight' })
      fireEvent.keyDown(document.body, { key: 'Delete' })
      fireEvent.keyDown(document.body, { key: 'z', metaKey: true })
      expect(label('b')).toContain('第 5 列第 1 行')
      expect(screen.getByRole('button', { name: /撤销$/ })).toBeDisabled()
      expect(screen.getByRole('button', { name: '新增 screen' })).toBeDisabled()
      expect(screen.getByTestId('layout-editor').querySelector('[inert]')).not.toBeNull()
      const put = api.calls.find((c) => c.method === 'PUT')!
      const sent = (put.body as { layout: Layout }).layout
      release(json(200, { version: 4, source: 'edit', created_at: '2026-10-01T00:00:00Z', layout: sent, broken: [] }))
      await waitFor(() => expect(screen.getByTestId('base-version')).toHaveTextContent('基于 v4'))
      expect(label('b')).toContain('第 5 列第 1 行')
      expect(screen.getByTestId('layout-editor').querySelector('[inert]')).toBeNull()
    })

    it('任一模态打开时 Cmd/Ctrl+Z 不撤销背后的草稿', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(widgetEl('b'))
      fireEvent.keyDown(document.body, { key: 'ArrowRight' })
      await user.click(screen.getByRole('button', { name: '放弃' }))
      await screen.findByRole('dialog')
      fireEvent.keyDown(document.body, { key: 'z', ctrlKey: true })
      expect(label('b')).toContain('第 5 列第 1 行')
      await user.click(screen.getByRole('button', { name: '继续编辑' }))
      fireEvent.keyDown(document.body, { key: 'z', ctrlKey: true })
      expect(label('b')).toContain('第 4 列第 1 行')
    })

    it('冲突响应没有数字版本号时不出现 NaN，沿用已知的最新版本', async () => {
      const user = userEvent.setup()
      let n = 0
      await mount((req) => {
        if (req.method !== 'PUT') return undefined
        n++
        return apiError(409, 'layout.conflict', n === 1 ? { latest_version: 7 } : {})
      })
      await user.click(widgetEl('b'))
      fireEvent.keyDown(document.body, { key: 'ArrowRight' })
      await user.click(screen.getByRole('button', { name: '保存并推送' }))
      const dlg = await screen.findByRole('dialog')
      await user.click(within(dlg).getByRole('button', { name: '以我的为准，基于最新版本重新提交' }))
      await waitFor(() => expect(screen.getByRole('dialog')).toHaveTextContent('最新版本又变了（v7）'))
      expect(document.body.textContent).not.toContain('NaN')
    })

    it('缩小网格后顶栏撤销连续两次回到已与 v3 一致', async () => {
      const user = userEvent.setup()
      await mount()
      await user.selectOptions(screen.getByRole('combobox', { name: '网格' }), '6x4')
      await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: /移除 1 个并缩小/ }))
      await user.click(screen.getByRole('button', { name: /撤销$/ }))
      await user.click(screen.getByRole('button', { name: /撤销$/ }))
      expect(screen.getByTestId('dirty-chip')).toHaveTextContent('已与 v3 一致')
      expect(widgetEl('far')).toBeInTheDocument()
    })

    it('网格已缩小时单条撤销被删小组件被拒，提示先撤销网格改动', async () => {
      const user = userEvent.setup()
      await mount()
      await user.selectOptions(screen.getByRole('combobox', { name: '网格' }), '6x4')
      await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: /移除 1 个并缩小/ }))
      const row = within(screen.getByTestId('changes-panel')).getAllByRole('listitem').find((r) => r.textContent?.includes('删除'))!
      await user.click(within(row).getByRole('button', { name: '撤销此条' }))
      expect(await screen.findByText(/请先撤销网格改动/)).toBeInTheDocument()
    })

    it('新增 screen：生成唯一 id、默认名可立即改，进入清单，可撤销', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(screen.getByRole('button', { name: '新增 screen' }))
      const input = screen.getByRole('textbox', { name: 'screen 名称' })
      expect(input).toHaveValue('屏幕 1')
      await user.clear(input)
      await user.type(input, '网络{Enter}')
      const tab = screen.getByRole('tab', { name: /screen1/ })
      expect(tab).toHaveTextContent('网络')
      expect(tab).toHaveAttribute('aria-selected', 'true')
      expect(screen.getByTestId('dirty-chip')).toHaveTextContent('未保存 1 处')
      await user.click(screen.getByRole('button', { name: '新增 screen' }))
      await user.keyboard('{Enter}')
      expect(screen.getByRole('tab', { name: /screen2/ })).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: /撤销$/ }))
      await user.click(screen.getByRole('button', { name: /撤销$/ }))
      expect(screen.queryByRole('tab', { name: /screen1/ })).toBeNull()
      expect(screen.getByTestId('dirty-chip')).toHaveTextContent('已与 v3 一致')
    })

    it('重命名 screen：进清单并可撤销；空名不提交', async () => {
      const user = userEvent.setup()
      await mount()
      await user.click(screen.getByRole('button', { name: '重命名 screen' }))
      const input = screen.getByRole('textbox', { name: 'screen 名称' })
      await user.clear(input)
      await user.keyboard('{Enter}')
      expect(screen.getByRole('tab', { name: /index/ })).toHaveTextContent('首页')
      await user.click(screen.getByRole('button', { name: '重命名 screen' }))
      await user.clear(screen.getByRole('textbox', { name: 'screen 名称' }))
      await user.type(screen.getByRole('textbox', { name: 'screen 名称' }), '总览{Enter}')
      expect(screen.getByRole('tab', { name: /index/ })).toHaveTextContent('总览')
      const row = within(screen.getByTestId('changes-panel')).getAllByRole('listitem')[0]
      expect(row).toHaveTextContent('首页')
      await user.click(within(row).getByRole('button', { name: '撤销此条' }))
      expect(screen.getByRole('tab', { name: /index/ })).toHaveTextContent('首页')
    })

    it('screen 数到上限时新增按钮禁用', async () => {
      const lay = layout()
      for (let i = 0; i < 30; i++) lay.screens.push({ id: `x${i}`, name: `n${i}`, dwell_seconds: 0, in_rotation: true, widgets: [] })
      await mount((req) => (req.method === 'GET' && req.url === '/api/screens' ? json(200, { ...layoutState(), layout: lay }) : undefined))
      expect(screen.getByRole('button', { name: '新增 screen' })).toBeDisabled()
    })
  })
})
