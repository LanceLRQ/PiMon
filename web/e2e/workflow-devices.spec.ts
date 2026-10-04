import { adminPassword, ensureAdminSetup } from './support/admin.ts'
import { collectConsoleErrors } from './support/checks.ts'
import { insertDevices } from './support/devices.ts'
import { expect, test } from './support/fixtures.ts'
import { startHub } from './support/hub.ts'

// 设备页：侧栏进入、中枢固定首行、造数据的设备出现在列表，筛选与搜索可用；手机宽度改为卡片。
// 设备经造数据钩子写库，hub 重启后处于启动宽限期（pending），所以这里不断言在线状态，只断言行、占位与版本标记。

test('设备页列出中枢与设备，筛选搜索可用，手机宽度显示卡片', async ({ browser }) => {
  test.setTimeout(120_000)
  const hub = await startHub({
    seed: (db) =>
      insertDevices(db, [
        { name: '飞牛 NAS', role: 'nas' },
        { name: '笔记本', role: 'dev', allowOffline: true },
        { name: '过旧 agent', role: 'server', incompatibleReason: 'version_too_old' },
        { name: '书房小主机', role: 'server', unconfirmed: true },
      ]),
  })
  const baseURL = hub.url
  const context = await browser.newContext({ baseURL, locale: 'zh-CN', viewport: { width: 1440, height: 900 } })
  try {
    await ensureAdminSetup(context.request, baseURL, hub.setupCode)
    const login = await context.request.post('/api/login', { data: { password: adminPassword }, headers: { Origin: baseURL } })
    expect(login.ok(), `API 登录失败：${login.status()}`).toBe(true)

    const page = await context.newPage()
    const errors = collectConsoleErrors(page)
    await page.goto('/')
    await page.getByRole('complementary', { name: '主导航' }).getByRole('link', { name: /设备/ }).click()
    await expect(page).toHaveURL(/\/devices$/)
    await expect(page.getByRole('heading', { name: '设备', level: 1 })).toBeVisible()

    const rows = page.locator('tbody tr[data-device-id]')
    await expect(rows).toHaveCount(5)
    await expect(rows.first()).toHaveAttribute('data-device-id', 'hub')
    await expect(rows.nth(1)).toContainText('飞牛 NAS')
    await expect(page.locator('tr', { hasText: '过旧 agent' })).toContainText('需要升级 agent')
    await expect(page.locator('tr', { hasText: '书房小主机' })).toContainText('等待连接')
    await expect(page.locator('tr', { hasText: '笔记本' })).toContainText('允许离线')

    await page.getByRole('searchbox', { name: '搜索设备' }).fill('飞牛')
    await expect(rows).toHaveCount(2)
    await page.getByRole('searchbox', { name: '搜索设备' }).fill('')
    await expect(rows).toHaveCount(5)

    // 深链：?open= 高亮对应行
    const nasId = await rows.nth(1).getAttribute('data-device-id')
    await page.goto(`/devices?open=${nasId}`)
    await expect(page.locator(`tr[data-device-id="${nasId}"]`)).toHaveAttribute('aria-current', 'true')

    // 手机宽度：卡片列表，没有表格，页面不横向滚动
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(page.getByRole('table')).toHaveCount(0)
    await expect(page.locator('main li[aria-current], main ul > li').first()).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect(errors).toEqual([])
  } finally {
    await context.close()
    await hub.dispose()
  }
})
