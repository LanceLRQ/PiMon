import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiError, defaultPlugins, json, mockApi, renderWithApp, seedStore, type ApiHandler } from '@/pages/instances/test-utils'
import { makeInstance } from '@/pages/instances/instances.fixtures'
import { liveStore } from '@/store/live-store'
import type { Layout, LayoutState, LayoutWidget, PluginInfo, ScreenStatus, WidgetCatalog } from '@/types/generated'
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
    it('缩小到会让小组件越界的网格时被拒绝并提示数量，网格保持不变', async () => {
      const user = userEvent.setup()
      await mount()
      const sel = screen.getByRole('combobox', { name: '网格' })
      expect(within(sel).getByRole('option', { name: /6×4 · 1 个越界/ })).toBeInTheDocument()
      await user.selectOptions(sel, '6x4')
      expect(await screen.findByText(/缩小到这个网格会让 1 个小组件越界/)).toBeInTheDocument()
      expect(sel).toHaveValue('8x5')
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
