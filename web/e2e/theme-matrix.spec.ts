import { mkdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import type { APIRequestContext, Page } from '@playwright/test'
import { adminPassword, ensureAdminSetup } from './support/admin.ts'
import { collectConsoleErrors, findScreenOverflow } from './support/checks.ts'
import { expect, test } from './support/fixtures.ts'
import { startHub } from './support/hub.ts'

// 主题矩阵（Ruling 29：不做像素基线，只逐项断言）：5 种屏幕 × 3 套主题 × {常规, 降低特效} × {默认首页, 极端数据页}。
// 每种屏幕自起一个干净的 hub：默认首页取 hub 按显示器自动选出的种子网格，互不影响，也不碰共享 hub 的布局。
// 每项断言：页面无滚动、每个小组件不溢出、屏幕页 console 零报错；截图只存为测试产物，不比对。
// 主题经时段计划切换，同一个页面不刷新，顺带验证「切主题无刷新」。

const themes = ['ambient', 'mission-control', 'industrial'] as const
type Theme = (typeof themes)[number]

interface Screen {
  name: string
  width: number
  height: number
  dpr: number
  grid: { cols: number; rows: number }
}

// 1920×1080 缩放 1.5 即 CSS 像素 1280×720（kiosk 用 --force-device-scale-factor=1.5 的等效）
const screens: Screen[] = [
  { name: '800x480', width: 800, height: 480, dpr: 1, grid: { cols: 6, rows: 4 } },
  { name: '1024x600', width: 1024, height: 600, dpr: 1, grid: { cols: 8, rows: 5 } },
  { name: '1280x720', width: 1280, height: 720, dpr: 1, grid: { cols: 10, rows: 6 } },
  { name: '1280x800', width: 1280, height: 800, dpr: 1, grid: { cols: 10, rows: 6 } },
  { name: '1920x1080@1.5', width: 1280, height: 720, dpr: 1.5, grid: { cols: 10, rows: 6 } },
]

// 极端页放进布局的小组件：坐标按 10x6 给出，放不进当前网格的自动略去。互不重叠。
interface Candidate {
  template: string
  col: number
  row: number
  w: number
  h: number
  item: string
  // 给出时用 demo 插件自带的小组件（通用 list 不展开通配，主机列表走插件小组件的 items 槽）
  widget?: string
}
const extremeCandidates: Candidate[] = [
  { template: 'list', col: 0, row: 0, w: 4, h: 3, item: 'host[*]', widget: 'hostlist' },
  { template: 'table', col: 4, row: 0, w: 2, h: 2, item: 'tasks' },
  { template: 'value', col: 4, row: 2, w: 1, h: 1, item: 'balance[cny]' },
  { template: 'value', col: 5, row: 2, w: 1, h: 1, item: 'balance[usd]' },
  { template: 'value', col: 4, row: 3, w: 1, h: 1, item: 'balance[eur]' },
  { template: 'value', col: 5, row: 3, w: 1, h: 1, item: 'balance[jpy]' },
  { template: 'state', col: 0, row: 3, w: 2, h: 1, item: 'alert.disk' },
  { template: 'gauge', col: 2, row: 3, w: 1, h: 1, item: 'mem.pi' },
  { template: 'gauge', col: 3, row: 3, w: 1, h: 1, item: 'cpu.pi' },
  { template: 'status-grid', col: 6, row: 0, w: 2, h: 2, item: 'host[*]' },
  { template: 'chart', col: 6, row: 2, w: 2, h: 2, item: 'cpu.pi' },
  { template: 'value', col: 0, row: 4, w: 2, h: 1, item: 'balance[cny]' },
  { template: 'value', col: 2, row: 4, w: 2, h: 1, item: 'balance[jpy]' },
  { template: 'state', col: 4, row: 4, w: 2, h: 1, item: 'alert.disk' },
  { template: 'gauge', col: 6, row: 4, w: 2, h: 1, item: 'mem.pi' },
  { template: 'status-grid', col: 8, row: 0, w: 2, h: 2, item: 'host[*]' },
  { template: 'gauge', col: 8, row: 2, w: 2, h: 2, item: 'mem.pi' },
  { template: 'table', col: 8, row: 4, w: 2, h: 2, item: 'tasks' },
  { template: 'state', col: 0, row: 5, w: 2, h: 1, item: 'alert.disk' },
  { template: 'value', col: 2, row: 5, w: 2, h: 1, item: 'balance[eur]' },
  { template: 'state', col: 4, row: 5, w: 2, h: 1, item: 'alert.disk' },
  { template: 'gauge', col: 6, row: 5, w: 2, h: 1, item: 'cpu.pi' },
]

// Ruling 21 的预算口径：服务端推送立即入队，断言放宽到 1.5 秒，实测耗时写进输出
const pushBudgetMs = 1500

const artifactDir = path.resolve(import.meta.dirname, '../test-results/theme-matrix')

function extremeScreen(instanceId: string, grid: { cols: number; rows: number }) {
  const widgets = extremeCandidates
    .filter((c) => c.col + c.w <= grid.cols && c.row + c.h <= grid.rows)
    .map((c, i) => ({
      id: `x${i}`,
      ...(c.widget
        ? { source: 'plugin', plugin_id: 'demo', widget_id: c.widget, binding: { instance_id: instanceId } }
        : { source: c.template === 'status-grid' ? 'aggregate' : 'generic', template: c.template, binding: { refs: [{ instance_id: instanceId, item: c.item }] } }),
      size: { cols: c.w, rows: c.h },
      col: c.col,
      row: c.row,
      options: { title: `${c.template} ${c.item}` },
    }))
  return { id: 'extreme', name: '极端数据', dwell_seconds: 0, in_rotation: false, widgets }
}

async function api<T>(request: APIRequestContext, method: 'get' | 'post' | 'put', url: string, baseURL: string, data?: unknown): Promise<T> {
  const res = await request[method](url, { data, headers: { Origin: baseURL } })
  if (!res.ok()) throw new Error(`${method.toUpperCase()} ${url} → ${res.status()} ${await res.text()}`)
  return (await res.json()) as T
}

// 等屏幕页的根元素属性变成目标值，返回从调用到生效的毫秒数
async function waitRoot(page: Page, attrs: { theme?: Theme; reduce?: boolean }, timeout: number): Promise<number> {
  const t0 = Date.now()
  await page.waitForFunction(
    ({ theme, reduce }) => {
      const root = document.documentElement
      return (theme === undefined || root.getAttribute('data-theme') === theme) && (reduce === undefined || root.hasAttribute('data-reduce-effects') === reduce)
    },
    attrs,
    { timeout },
  )
  return Date.now() - t0
}

for (const scr of screens) {
  test(`主题矩阵 ${scr.name}：3 主题 × {常规, 降低特效} × {默认首页, 极端数据页} 无滚动、不溢出、零报错`, async ({ browser }, testInfo) => {
    test.setTimeout(240_000)
    const hub = await startHub()
    const baseURL = hub.url
    const context = await browser.newContext({
      baseURL,
      locale: 'zh-CN',
      viewport: { width: scr.width, height: scr.height },
      deviceScaleFactor: scr.dpr,
    })
    const admin = await browser.newContext({ baseURL })
    try {
      await ensureAdminSetup(admin.request, baseURL, hub.setupCode)
      await api(admin.request, 'post', '/api/login', baseURL, { password: adminPassword })
      const instance = await api<{ id: string }>(admin.request, 'post', '/api/instances', baseURL, {
        plugin_id: 'demo',
        name: '极端数据',
        config: { profile: 'extreme' },
        interval_seconds: 0,
      })
      // 先同步采集一次，让屏幕首屏就有数据（种子实例由 hub 按计划自行采集）
      await api(admin.request, 'post', `/api/instances/${instance.id}/run`, baseURL)
      const seeded = await api<{ id: string }[]>(admin.request, 'get', '/api/instances', baseURL)
      for (const inst of seeded) if (inst.id !== instance.id) await admin.request.post(`/api/instances/${inst.id}/run`, { headers: { Origin: baseURL } })

      const page = await context.newPage()
      const errors = collectConsoleErrors(page)
      const token = readFileSync(path.join(hub.dataDir, 'screen.token'), 'utf8').trim()
      await page.goto(`/screen/auth?token=${encodeURIComponent(token)}`)
      await expect(page.locator('[data-screen-grid]')).toBeVisible()

      // 显示器视口被采信后，hub 把仍是种子原版的布局换成该屏幕推荐网格的种子布局
      await expect
        .poll(async () => (await api<{ layout: { grid: { cols: number; rows: number } } }>(admin.request, 'get', '/api/screens', baseURL)).layout.grid, { timeout: 20_000, intervals: [500] })
        .toEqual(scr.grid)
      await expect(page.locator('[data-widget-id]').first()).toBeVisible()
      await page.evaluate(() => {
        ;(window as unknown as { __matrixMarker: number }).__matrixMarker = 1
      })

      const shots = path.join(artifactDir, scr.name)
      mkdirSync(shots, { recursive: true })
      const pushTimes: number[] = []
      const settingsTimes: number[] = []
      const failures: string[] = []

      const settings = await api<Record<string, unknown>>(admin.request, 'get', '/api/settings', baseURL)
      const setReduce = async (reduce: boolean) => {
        await api(admin.request, 'put', '/api/settings', baseURL, { ...settings, reduce_effects: reduce })
      }
      const setTheme = async (theme: Theme) => {
        await api(admin.request, 'put', '/api/schedule', baseURL, { periods: [{ start: '00:00', end: '00:00', theme }] })
      }

      const inspect = async (label: string, shotName: string) => {
        // 等一帧布局与字体稳定，再取检查结果与截图
        await page.evaluate(() => document.fonts.ready.then(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))))
        const offenders = await findScreenOverflow(page)
        if (offenders.length > 0) failures.push(`${label}\n    ${offenders.join('\n    ')}`)
        await page.screenshot({ path: path.join(shots, `${shotName}.png`) })
      }

      const runPage = async (kind: 'home' | 'extreme') => {
        for (const reduce of [false, true]) {
          // 先把「降低特效」切到目标值，再轮流切三个主题
          // 设置走 1 秒合并窗口，不在 Ruling 21 的立即推送之列，只记录耗时，预算放宽到 4 秒
          await setReduce(reduce)
          settingsTimes.push(await waitRoot(page, { reduce }, 4000))
          for (const theme of themes) {
            await setTheme(theme)
            pushTimes.push(await waitRoot(page, { theme, reduce }, pushBudgetMs))
            await inspect(`${scr.name} ${kind} ${theme}${reduce ? ' 降低特效' : ''}`, `${kind}-${theme}${reduce ? '-reduced' : ''}`)
          }
        }
      }

      await runPage('home')

      // 极端页：加一个不参与轮播的 screen 并远程切过去
      const cur = await api<{ version: number; layout: { grid: { cols: number; rows: number }; screens: unknown[] } }>(admin.request, 'get', '/api/screens', baseURL)
      await api(admin.request, 'put', '/api/screens', baseURL, {
        base_version: cur.version,
        layout: { ...cur.layout, screens: [...cur.layout.screens, extremeScreen(instance.id, cur.layout.grid)] },
      })
      await api(admin.request, 'post', '/api/screen/control', baseURL, { action: 'switch', screen_id: 'extreme' })
      await expect(page.locator('[data-screen-grid][data-screen-id="extreme"]')).toBeVisible({ timeout: pushBudgetMs + 1000 })
      // 极端数据确实到位（否则检查是空转）：主机列表有行，且屏幕上的小组件数等于布局里的
      await expect(page.locator('section[data-widget-id="x0"] .tpl-list__row').first()).toBeVisible()
      const expectedWidgets = extremeScreen(instance.id, cur.layout.grid).widgets.length
      await expect.poll(() => page.locator('section[data-widget-id]').count()).toBe(expectedWidgets)
      await runPage('extreme')

      // 页面始终没有刷新
      expect(await page.evaluate(() => (window as unknown as { __matrixMarker?: number }).__matrixMarker)).toBe(1)
      const msg = `${scr.name}：${pushTimes.length} 次主题推送最慢 ${Math.max(...pushTimes)} ms（预算 ${pushBudgetMs} ms），${settingsTimes.length} 次降低特效设置最慢 ${Math.max(...settingsTimes)} ms`
      testInfo.annotations.push({ type: '主题推送耗时', description: msg })
      console.log(msg)
      expect(failures, failures.join('\n')).toEqual([])
      expect(errors).toEqual([])
    } finally {
      await admin.close()
      await context.close()
      await hub.dispose()
    }
  })
}
