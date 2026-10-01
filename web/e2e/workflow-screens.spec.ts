import { adminPassword, ensureAdminSetup } from './support/admin.ts'
import { collectConsoleErrors } from './support/checks.ts'
import { expect, test } from './support/fixtures.ts'

// 管理员由 ensureAdminSetup 保证，可单独运行；用例结束前把布局还原（新增的 screen 删掉），不影响其他用例。

interface ScreenStatus {
  online: boolean
  viewport?: { w: number; h: number }
  state: { mode: string }
}
interface LayoutState {
  version: number
  layout: { screens: { id: string; in_rotation: boolean }[] }
}

test('screens 管理页：新增、排序、保存后版本摘要记录重排，再删除还原；总览屏幕卡与系统页屏幕区；管理员预览不影响显示器状态', async ({ browser, baseURL }) => {
  const context = await browser.newContext({ baseURL, locale: 'zh-CN', viewport: { width: 1440, height: 900 } })
  try {
    await ensureAdminSetup(context.request, baseURL!)
    const login = await context.request.post('/api/login', { data: { password: adminPassword }, headers: { Origin: baseURL! } })
    expect(login.ok(), `API 登录失败：${login.status()}`).toBe(true)
    const getJson = async <T>(url: string) => (await (await context.request.get(url)).json()) as T
    const page = await context.newPage()
    const errors = collectConsoleErrors(page)

    // 新增两个 screen 并保存
    await page.goto('/screens')
    const table = page.getByTestId('screen-table')
    await expect(table).toBeVisible()
    await expect(page.getByTestId('screen-thumb').first()).toBeVisible()
    const initialRows = await table.getByRole('row').count()
    await page.getByRole('button', { name: '新增 screen' }).click()
    await page.getByRole('button', { name: '新增 screen' }).click()
    await expect(table.getByRole('row')).toHaveCount(initialRows + 2)
    const startVersion = (await getJson<LayoutState>('/api/screens')).version
    await page.getByTestId('screens-dirty-bar').getByRole('button', { name: '保存' }).click()
    await expect(page.getByTestId('screens-dirty-bar')).toHaveCount(0)
    const afterAdd = await getJson<LayoutState>('/api/screens')
    expect(afterAdd.version).toBe(startVersion + 1)
    const ids = afterAdd.layout.screens.map((s) => s.id)
    const [first, second] = ids.slice(-2)

    // 手柄上按方向键，把最后一个 screen 排到前一个之前；保存后顺序落库，版本摘要标记重排
    await page.getByRole('button', { name: `${second} 排序手柄` }).focus()
    await page.keyboard.press('ArrowUp')
    await expect(page.getByTestId('screens-changes')).toContainText('调整了 screen 的轮播顺序')
    await page.getByTestId('screens-dirty-bar').getByRole('button', { name: '保存' }).click()
    await expect(page.getByTestId('screens-dirty-bar')).toHaveCount(0)
    const reordered = await getJson<LayoutState>('/api/screens')
    expect(reordered.layout.screens.map((s) => s.id).slice(-2)).toEqual([second, first])
    const versions = await getJson<{ version: number; summary: { reordered: boolean } }[]>('/api/screens/versions')
    expect(versions[0].version).toBe(reordered.version)
    expect(versions[0].summary.reordered).toBe(true)

    // 删除这两个 screen（二次确认）并保存，还原
    for (const id of [first, second]) {
      await page.getByRole('button', { name: `删除 ${id}` }).click()
      await page.getByRole('dialog').getByRole('button', { name: '删除' }).click()
    }
    await page.getByTestId('screens-dirty-bar').getByRole('button', { name: '保存' }).click()
    await expect(page.getByTestId('screens-dirty-bar')).toHaveCount(0)
    expect((await getJson<LayoutState>('/api/screens')).layout.screens.map((s) => s.id)).toEqual(ids.slice(0, -2))

    // 总览屏幕卡：缩略图、按键；k3 关屏再开屏，状态跟着变
    await page.goto('/')
    const card = page.getByRole('region', { name: '屏幕' })
    await expect(card.getByTestId('screen-thumb')).toBeVisible()
    await card.getByRole('button', { name: /关屏/ }).click()
    await expect.poll(async () => (await getJson<ScreenStatus>('/api/screen/status')).state.mode).toBe('off')
    await expect(card.getByText('屏幕已关闭')).toBeVisible()
    await card.getByRole('button', { name: /开屏/ }).click()
    await expect.poll(async () => (await getJson<ScreenStatus>('/api/screen/status')).state.mode).toBe('on')

    // 系统页屏幕区
    await page.goto('/system')
    await expect(page.getByRole('region', { name: '屏幕' }).getByRole('link', { name: '在屏幕管理里调整' })).toBeVisible()

    // 管理员预览 /screen：有标识与真实网格；不上报尺寸，不改变显示器在线状态与已采信的 viewport
    const before = await getJson<ScreenStatus>('/api/screen/status')
    await page.goto('/screen')
    await expect(page.getByTestId('admin-preview-badge')).toHaveText('管理员预览')
    await expect(page.locator('[data-screen-grid]')).toBeVisible()
    await page.setViewportSize({ width: 800, height: 480 })
    // 服务端要求尺寸稳定 2 秒才采信：等过这个时间再看
    await page.waitForTimeout(3500)
    const after = await getJson<ScreenStatus>('/api/screen/status')
    expect(after.online).toBe(before.online)
    expect(after.viewport).toEqual(before.viewport)

    expect(errors).toEqual([])
  } finally {
    await context.close()
  }
})
