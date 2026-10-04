import { readFileSync } from 'node:fs'
import path from 'node:path'
import type { APIRequestContext } from '@playwright/test'
import { adminPassword, ensureAdminSetup } from './support/admin.ts'
import { collectConsoleErrors } from './support/checks.ts'
import { expect, test } from './support/fixtures.ts'
import { startHub } from './support/hub.ts'

// 编辑器添加主机灯组的端到端路径：从小组件库拖入「主机灯组」→ 保存成功 → 屏幕上出现灯组。
// 自起干净 hub，不改共享 hub 的布局。种子首页已含灯组，所以先经接口把它摘掉，再在编辑器里重新添加。

interface Widget {
  id: string
  binding: { data_source?: string }
  size: { cols: number; rows: number }
  col: number
  row: number
}
interface ScreensDoc {
  version: number
  layout: { grid: { cols: number; rows: number }; screens: { id: string; widgets: Widget[] }[] }
}

async function getScreens(request: APIRequestContext, baseURL: string): Promise<ScreensDoc> {
  const res = await request.get('/api/screens', { headers: { Origin: baseURL } })
  expect(res.ok()).toBe(true)
  return (await res.json()) as ScreensDoc
}

test('编辑器从小组件库添加主机灯组，保存后屏幕出现灯组，两边 console 零报错', async ({ browser }) => {
  test.setTimeout(120_000)
  const hub = await startHub()
  const baseURL = hub.url
  const screenContext = await browser.newContext({ baseURL, locale: 'zh-CN', viewport: { width: 1024, height: 600 } })
  const adminContext = await browser.newContext({ baseURL, locale: 'zh-CN', viewport: { width: 1440, height: 900 } })
  try {
    await ensureAdminSetup(adminContext.request, baseURL, hub.setupCode)
    const login = await adminContext.request.post('/api/login', { data: { password: adminPassword }, headers: { Origin: baseURL } })
    expect(login.ok(), `API 登录失败：${login.status()}`).toBe(true)

    // 屏幕上线，让显示器视口稳定为 1024×600（8×5 网格）；种子首页里本来有灯组
    const screenPage = await screenContext.newPage()
    const screenErrors = collectConsoleErrors(screenPage)
    const token = readFileSync(path.join(hub.dataDir, 'screen.token'), 'utf8').trim()
    await screenPage.goto(`/screen/auth?token=${encodeURIComponent(token)}`)
    await expect(screenPage.locator('[data-screen-grid]')).toBeVisible()
    await expect.poll(async () => (await getScreens(adminContext.request, baseURL)).layout.grid, { timeout: 20_000, intervals: [500] }).toEqual({ cols: 8, rows: 5 })
    const hostsOnScreen = screenPage.locator('[data-widget-frame][data-template="status-grid"]:has([data-host-state])')
    await expect(hostsOnScreen.first()).toBeVisible()

    // 经接口摘掉种子灯组，腾出 2×2 的位置
    const cur = await getScreens(adminContext.request, baseURL)
    const home = cur.layout.screens[0]
    const seeded = home.widgets.find((w) => w.binding.data_source === 'hosts')
    expect(seeded, '种子首页应含主机灯组').toBeTruthy()
    expect(seeded!.size).toEqual({ cols: 2, rows: 2 })
    const put = await adminContext.request.put('/api/screens', {
      data: { base_version: cur.version, layout: { ...cur.layout, screens: [{ ...home, widgets: home.widgets.filter((w) => w.id !== seeded!.id) }, ...cur.layout.screens.slice(1)] } },
      headers: { Origin: baseURL },
    })
    expect(put.ok(), `摘掉灯组失败：${put.status()}`).toBe(true)
    await expect(hostsOnScreen).toHaveCount(0)

    // 编辑器：小组件库里有「主机灯组」，拖到腾出的位置
    const page = await adminContext.newPage()
    const errors = collectConsoleErrors(page)
    await page.goto('/screens/editor')
    await expect(page.getByTestId('layout-editor')).toBeVisible()
    const bezel = page.getByTestId('editor-bezel')
    await expect(bezel).toBeVisible()
    const box = (await bezel.boundingBox())!
    const cw = box.width / 8
    const ch = box.height / 5
    // 小组件库按「插件 / 通用 / 聚合」分页，灯组在聚合页
    await page.locator('#lib-tab-aggregate').click()
    await expect(page.getByTestId('lib-item-aggregate:status-grid:hosts')).toBeVisible()
    await page.getByTestId('lib-item-aggregate:status-grid:hosts').dragTo(bezel, { targetPosition: { x: (seeded!.col + 0.5) * cw, y: (seeded!.row + 0.5) * ch } })
    const added = page.locator('[data-editor-widget][aria-pressed="true"]')
    await expect(added).toHaveCount(1)
    await expect(page.getByTestId('dirty-chip')).toHaveText(/未保存 1 处/)

    // 保存成功：接口 200，布局里多出一个 data_source 为 hosts 的 2×2 小组件
    const saved = page.waitForResponse((r) => r.url().endsWith('/api/screens') && r.request().method() === 'PUT')
    await page.getByRole('button', { name: '保存并推送' }).click()
    expect((await saved).status()).toBe(200)
    await expect(page.getByTestId('dirty-chip')).toHaveAttribute('data-dirty', 'false')
    const after = await getScreens(adminContext.request, baseURL)
    const hostsWidgets = after.layout.screens[0].widgets.filter((w) => w.binding.data_source === 'hosts')
    expect(hostsWidgets).toHaveLength(1)
    expect(hostsWidgets[0].size).toEqual({ cols: 2, rows: 2 })

    // 屏幕上重新出现灯组：中枢一格在线
    await expect(hostsOnScreen).toHaveCount(1, { timeout: 5000 })
    await expect(hostsOnScreen.locator('[data-host-state="online"]').first()).toBeVisible()

    expect(errors).toEqual([])
    expect(screenErrors).toEqual([])
  } finally {
    await adminContext.close()
    await screenContext.close()
    await hub.dispose()
  }
})
