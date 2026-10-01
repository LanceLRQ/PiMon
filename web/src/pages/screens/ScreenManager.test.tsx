import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiError, defaultPlugins, json, mockApi, renderWithApp, seedStore, type ApiHandler, type Req } from '@/pages/instances/test-utils'
import { liveStore } from '@/store/live-store'
import type { Layout, LayoutState, LayoutVersionInfo, LayoutWidget, ScreenStatus, Settings } from '@/types/generated'
import { ScreenManager } from './ScreenManager'

beforeEach(() => {
  liveStore.reset()
  localStorage.clear()
})
afterEach(() => vi.unstubAllGlobals())

const sz = (cols: number, rows: number) => ({ cols, rows })
const widget = (id: string, col: number, row: number): LayoutWidget => ({
  id, source: 'generic', template: 'value', size: sz(1, 1), col, row, binding: {}, options: { title: `组件${id}` },
})
const layout = (): Layout => ({
  grid: { cols: 8, rows: 5 },
  screens: [
    { id: 'index', name: '首页', dwell_seconds: 0, in_rotation: true, widgets: [widget('a', 0, 0), widget('far', 7, 4)] },
    { id: 's1', name: '主机', dwell_seconds: 0, in_rotation: true, widgets: [] },
    { id: 's2', name: '网络', dwell_seconds: 20, in_rotation: false, widgets: [] },
  ],
})
const layoutState = (version = 5): LayoutState => ({ version, source: 'edit', created_at: '2026-10-01T00:00:00Z', layout: layout(), broken: [] })
const settings = (): Settings =>
  ({
    language: 'zh', timezone: 'Asia/Shanghai', access_url: '', https_enabled: false, trusted_proxies: [], reduce_effects: false,
    screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'auto', ui_scale: 1 },
    retention: { raw_hours: 24, five_min_days: 30, hour_days: 365 }, backup: { daily_at: '04:00', keep: 7 },
  }) as Settings
const status = (over: Partial<ScreenStatus> = {}): ScreenStatus => ({
  state: { mode: 'on', theme_id: 'ambient', reason: 'schedule' },
  viewport: { w: 1024, h: 600, dpr: 1 },
  coarse_pointer: false,
  online: true,
  recommended_grid: sz(8, 5),
  ...over,
})
const versions: LayoutVersionInfo[] = [
  { version: 5, source: 'edit', created_at: '2026-10-01T08:00:00Z', summary: { changed_screens: ['s1'], widgets_added: 0, widgets_removed: 0, widgets_changed: 0, grid_changed: false, reordered: false }, has_broken: false },
  { version: 4, source: 'edit', created_at: '2026-09-30T08:00:00Z', summary: { changed_screens: ['index', 's2'], widgets_added: 0, widgets_removed: 0, widgets_changed: 1, grid_changed: false, reordered: false }, has_broken: false },
]

function handler(over: ApiHandler = () => undefined, st: ScreenStatus | null = status()): ApiHandler {
  return (req) => {
    const custom = over(req)
    if (custom) return custom
    const path = req.url.split('?')[0]
    if (req.method === 'GET' && path === '/api/screens') return json(200, layoutState())
    if (req.method === 'GET' && path === '/api/settings') return json(200, settings())
    if (req.method === 'GET' && path === '/api/screen/status') return st ? json(200, st) : apiError(500, 'internal')
    if (req.method === 'GET' && path === '/api/screens/versions') return json(200, versions)
    if (req.method === 'GET' && path === '/api/plugins') return json(200, defaultPlugins)
    return undefined
  }
}

async function mount(over?: ApiHandler, st?: ScreenStatus | null) {
  const api = mockApi(handler(over, st))
  seedStore([])
  await renderWithApp(<ScreenManager />)
  await screen.findByTestId('screen-table')
  return api
}

const row = (id: string) => screen.getByTestId(`screen-row-${id}`)
const order = () => within(screen.getByTestId('screen-table')).getAllByRole('row').slice(1).map((r) => r.getAttribute('data-testid')!.replace('screen-row-', ''))
const lastPut = (calls: Req[], url: string) => calls.filter((c) => c.method === 'PUT' && c.url === url).at(-1)

describe('screens 管理页', () => {
  it('列出全部 screen：index 置顶不可删、不可取消轮播，其余可操作；上次修改取自版本记录', async () => {
    await mount()
    expect(order()).toEqual(['index', 's1', 's2'])
    expect(within(row('index')).getByText('首页 · 不可删')).toBeInTheDocument()
    expect(within(row('index')).queryByRole('button', { name: '删除 index' })).toBeNull()
    expect(within(row('index')).getByRole('switch', { name: 'index 参与轮播' })).toBeDisabled()
    expect(within(row('index')).getByRole('button', { name: 'index 排序手柄' })).toBeDisabled()
    expect(within(row('s1')).getByRole('button', { name: '删除 s1' })).toBeInTheDocument()
    expect(within(row('s1')).getByText(/^v5 · /)).toBeInTheDocument()
    expect(within(row('s2')).getByText(/^v4 · /)).toBeInTheDocument()
    expect(within(row('s2')).getByRole('switch', { name: 's2 参与轮播' })).toHaveAttribute('aria-checked', 'false')
    expect(within(row('index')).getByText('2 个')).toBeInTheDocument()
    expect(screen.getAllByTestId('screen-thumb')).toHaveLength(3)
    expect(screen.queryByTestId('screens-dirty-bar')).toBeNull()
  })

  it('改轮播开关与停留后出现未保存清单，保存带 base_version 与整份布局', async () => {
    const user = userEvent.setup()
    const api = await mount((req) => (req.method === 'PUT' && req.url === '/api/screens' ? json(200, layoutState(6)) : undefined))
    await user.click(within(row('s1')).getByRole('switch', { name: 's1 参与轮播' }))
    const dwell = within(row('s1')).getByRole('spinbutton', { name: 's1 停留秒数' })
    await user.type(dwell, '45')
    const bar = screen.getByTestId('screens-dirty-bar')
    expect(within(bar).getByText('未保存 2 处改动')).toBeInTheDocument()
    expect(within(bar).getByText('s1 退出轮播')).toBeInTheDocument()
    expect(within(bar).getByText('s1 停留：默认 → 45')).toBeInTheDocument()
    await user.click(within(bar).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByTestId('screens-dirty-bar')).toBeNull())
    const put = lastPut(api.calls, '/api/screens')!.body as { base_version: number; layout: Layout }
    expect(put.base_version).toBe(5)
    const s1 = put.layout.screens.find((s) => s.id === 's1')!
    expect(s1).toMatchObject({ in_rotation: false, dwell_seconds: 45 })
    expect(lastPut(api.calls, '/api/settings')).toBeUndefined()
  })

  it('停留秒数无效时标红且不改草稿，失焦后还原', async () => {
    const user = userEvent.setup()
    await mount()
    const dwell = within(row('s1')).getByRole('spinbutton', { name: 's1 停留秒数' })
    await user.type(dwell, '2')
    expect(dwell).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByTestId('screens-dirty-bar')).toBeNull()
    await user.tab()
    expect(dwell).toHaveValue(null)
    expect(dwell).not.toHaveAttribute('aria-invalid')
  })

  it('拖拽排序后保存的顺序就是轮播顺序，index 不能被拖动或被排到前面', async () => {
    const api = await mount((req) => (req.method === 'PUT' && req.url === '/api/screens' ? json(200, layoutState(6)) : undefined))
    const dt = { setData: vi.fn(), effectAllowed: '' }
    expect(row('index')).toHaveAttribute('draggable', 'false')
    fireEvent.dragStart(row('s2'), { dataTransfer: dt })
    fireEvent.dragOver(row('index'), { dataTransfer: dt })
    fireEvent.drop(row('index'), { dataTransfer: dt })
    expect(order()).toEqual(['index', 's2', 's1'])
    expect(within(screen.getByTestId('screens-changes')).getByText('调整了 screen 的轮播顺序')).toBeInTheDocument()
    await userEvent.click(within(screen.getByTestId('screens-dirty-bar')).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastPut(api.calls, '/api/screens')).toBeDefined())
    const put = lastPut(api.calls, '/api/screens')!.body as { layout: Layout }
    expect(put.layout.screens.map((s) => s.id)).toEqual(['index', 's2', 's1'])
  })

  it('手柄上按方向键也能调整顺序', async () => {
    const user = userEvent.setup()
    await mount()
    row('s1').querySelector<HTMLElement>('button')!.focus()
    await user.keyboard('{ArrowDown}')
    expect(order()).toEqual(['index', 's2', 's1'])
    await user.keyboard('{ArrowUp}{ArrowUp}')
    // 已经在 index 下面第一位，再往上不动
    expect(order()).toEqual(['index', 's1', 's2'])
  })

  it('删除要二次确认，确认后从草稿里去掉，放弃改动可恢复', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(within(row('s1')).getByRole('button', { name: '删除 s1' }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByText(/screen「主机」及其中的 0 个小组件/)).toBeInTheDocument()
    await user.click(within(dlg).getByRole('button', { name: '删除' }))
    expect(order()).toEqual(['index', 's2'])
    expect(within(screen.getByTestId('screens-changes')).getByText('删除 screen s1「主机」')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '放弃改动' }))
    expect(order()).toEqual(['index', 's1', 's2'])
  })

  it('新增 screen 沿用编辑器的命名，未保存时标注', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(screen.getByRole('button', { name: '新增 screen' }))
    expect(order()).toEqual(['index', 's1', 's2', 'screen1'])
    expect(within(row('screen1')).getByText('未保存')).toBeInTheDocument()
    expect(within(row('screen1')).getByText('屏幕 1')).toBeInTheDocument()
  })

  it('重命名：点击名称改，空名称被拒绝', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(within(row('s1')).getByRole('button', { name: 's1 的名称' }))
    const input = screen.getByRole('textbox', { name: 's1 的名称' })
    await user.clear(input)
    await user.type(input, '服务器{Enter}')
    expect(within(row('s1')).getByText('服务器')).toBeInTheDocument()
    await user.click(within(row('s1')).getByRole('button', { name: 's1 的名称' }))
    const again = screen.getByRole('textbox', { name: 's1 的名称' })
    await user.clear(again)
    await user.type(again, '   {Enter}')
    expect(within(row('s1')).getByText('服务器')).toBeInTheDocument()
  })

  it('网格步进器缩小到有小组件越界时弹出共享越界对话框，确认后移除，取消保持', async () => {
    const user = userEvent.setup()
    await mount()
    await user.click(screen.getByRole('button', { name: '减列' }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByText('缩小到 7×5 会让 1 个小组件越界')).toBeInTheDocument()
    expect(within(dlg).getByText('组件far')).toBeInTheDocument()
    await user.click(within(dlg).getByRole('button', { name: '保持现有网格' }))
    expect(screen.queryByTestId('screens-dirty-bar')).toBeNull()
    await user.click(screen.getByRole('button', { name: '减列' }))
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '移除 1 个并缩小' }))
    const bar = screen.getByTestId('screens-dirty-bar')
    expect(within(bar).getByText('网格 8×5 → 7×5')).toBeInTheDocument()
    expect(within(row('index')).getByText('1 个')).toBeInTheDocument()
  })

  it('网格加大不弹对话框；预设与每格尺寸联动，推荐值带星标', async () => {
    const user = userEvent.setup()
    await mount()
    expect(screen.getByRole('radio', { name: '8×5 · 1024×600' })).toHaveTextContent('★')
    expect(screen.getByTestId('cell-info')).toHaveTextContent('每格 128×120 px')
    await user.click(screen.getByRole('radio', { name: '10×6 · 1280×720' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByTestId('cell-info')).toHaveTextContent('每格 102×100 px')
    expect(within(screen.getByTestId('screens-changes')).getByText('网格 8×5 → 10×6')).toBeInTheDocument()
  })

  it('保存遇到 409：提示最新版本，重新加载后回到服务端版本', async () => {
    const user = userEvent.setup()
    let conflict = true
    await mount((req) => {
      if (req.method === 'PUT' && req.url === '/api/screens') return apiError(409, 'layout.conflict', { latest_version: 9 })
      if (req.method === 'GET' && req.url === '/api/screens' && !conflict) return json(200, { ...layoutState(9), layout: { ...layout(), screens: layout().screens.slice(0, 2) } })
      return undefined
    })
    await user.click(within(row('s1')).getByRole('switch', { name: 's1 参与轮播' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    const note = await screen.findByText(/布局已被修改：你基于 v5，最新是 v9/)
    expect(note).toBeInTheDocument()
    expect(screen.getByTestId('screens-dirty-bar')).toBeInTheDocument()
    conflict = false
    await user.click(screen.getByRole('button', { name: '重新加载最新布局' }))
    await waitFor(() => expect(order()).toEqual(['index', 's1']))
    expect(screen.queryByText(/布局已被修改/)).toBeNull()
    expect(screen.queryByTestId('screens-dirty-bar')).toBeNull()
  })

  it('409 后重新加载只重置布局草稿，设置草稿保留', async () => {
    const user = userEvent.setup()
    await mount((req) => (req.method === 'PUT' && req.url === '/api/screens' ? apiError(409, 'layout.conflict', { latest_version: 9 }) : undefined))
    await user.click(within(row('s1')).getByRole('switch', { name: 's1 参与轮播' }))
    await user.click(screen.getByRole('radio', { name: '1.5×' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    await screen.findByText(/布局已被修改/)
    await user.click(screen.getByRole('button', { name: '重新加载最新布局' }))
    await waitFor(() => expect(screen.queryByText(/布局已被修改/)).toBeNull())
    expect(within(row('s1')).getByRole('switch', { name: 's1 参与轮播' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: '1.5×' })).toHaveAttribute('aria-checked', 'true')
    expect(within(screen.getByTestId('screens-changes')).getByText('屏幕显示参数（轮播、输入方式或界面缩放）')).toBeInTheDocument()
  })

  it('布局保存成功、设置保存失败：提示里说明布局已保存，设置仍为未保存', async () => {
    const user = userEvent.setup()
    await mount((req) => {
      if (req.method === 'PUT' && req.url === '/api/screens') return json(200, layoutState(6))
      if (req.method === 'PUT' && req.url === '/api/settings') return apiError(500, 'internal')
      return undefined
    })
    await user.click(within(row('s1')).getByRole('switch', { name: 's1 参与轮播' }))
    await user.click(screen.getByRole('radio', { name: '1.5×' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/布局已保存（v6），但设置保存失败/)).toBeInTheDocument()
    const bar = screen.getByTestId('screens-dirty-bar')
    expect(within(bar).getByText('未保存 1 处改动')).toBeInTheDocument()
  })

  it('每行「编辑布局」带上 screen 参数', async () => {
    await mount()
    expect(within(row('s1')).getByRole('link', { name: '编辑布局' })).toHaveAttribute('href', '/screens/editor?screen=s1')
    expect(within(row('index')).getByRole('link', { name: '编辑布局' })).toHaveAttribute('href', '/screens/editor?screen=index')
  })

  it('layout.invalid 给出提示且保留草稿', async () => {
    const user = userEvent.setup()
    await mount((req) => (req.method === 'PUT' && req.url === '/api/screens' ? apiError(422, 'layout.invalid', { problems: [{}, {}] }) : undefined))
    await user.click(within(row('s1')).getByRole('switch', { name: 's1 参与轮播' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('布局不合法（2 处），请检查后重试')).toBeInTheDocument()
    expect(screen.getByTestId('screens-dirty-bar')).toBeInTheDocument()
  })

  it('无触摸时选「只显示首页」给出提示；有触摸或手动指定有触摸时不提示', async () => {
    const user = userEvent.setup()
    await mount()
    expect(screen.queryByText(/当前屏幕无触摸：选择/)).toBeNull()
    await user.click(screen.getByRole('radio', { name: /只显示首页/ }))
    expect(screen.getByText(/当前屏幕无触摸：选择「只显示首页」后/)).toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: '有触摸' }))
    expect(screen.queryByText(/当前屏幕无触摸：选择/)).toBeNull()
  })

  it('屏幕没上报触摸能力时不乱提示', async () => {
    const user = userEvent.setup()
    await mount(undefined, status({ coarse_pointer: undefined }))
    await user.click(screen.getByRole('radio', { name: /只显示首页/ }))
    expect(screen.queryByText(/当前屏幕无触摸：选择/)).toBeNull()
    expect(screen.getByText(/屏幕还没有上报触摸能力/)).toBeInTheDocument()
  })

  it('界面缩放只保存设置（先取最新设置再整份 PUT），并提示由 kiosk 重启 Chromium 后生效', async () => {
    const user = userEvent.setup()
    const api = await mount((req) => {
      if (req.method === 'PUT' && req.url === '/api/settings') return json(200, { ...settings(), screen: { ...settings().screen, ui_scale: 1.5 } })
      return undefined
    })
    expect(screen.getByText('保存只写入设置，需由 kiosk 重启 Chromium 后生效。')).toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: '1.5×' }))
    expect(within(screen.getByTestId('screens-changes')).getByText('屏幕显示参数（轮播、输入方式或界面缩放）')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByTestId('screens-dirty-bar')).toBeNull())
    expect(lastPut(api.calls, '/api/screens')).toBeUndefined()
    const body = lastPut(api.calls, '/api/settings')!.body as Settings
    expect(body.screen.ui_scale).toBe(1.5)
    expect(body.timezone).toBe('Asia/Shanghai')
    expect(body.backup.keep).toBe(7)
  })

  it('默认停留与空闲回首页用步进器，写入设置', async () => {
    const user = userEvent.setup()
    const api = await mount((req) => (req.method === 'PUT' && req.url === '/api/settings' ? json(200, settings()) : undefined))
    await user.click(within(screen.getByRole('group', { name: '默认停留' })).getByRole('button', { name: '加' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(lastPut(api.calls, '/api/settings')).toBeDefined())
    const body = lastPut(api.calls, '/api/settings')!.body as Settings
    expect(body.screen.default_dwell_seconds).toBe(20)
  })

  it('加载失败显示错误与重试', async () => {
    mockApi((req) => (req.url === '/api/screens' ? apiError(500, 'internal') : undefined))
    seedStore([])
    await renderWithApp(<ScreenManager />)
    expect(await screen.findByRole('alert')).toHaveTextContent('无法加载屏幕配置')
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })
})
