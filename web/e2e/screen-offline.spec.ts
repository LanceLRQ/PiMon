import { readFileSync } from 'node:fs'
import path from 'node:path'
import { ensureAdminSetup } from './support/admin.ts'
import { expect, test } from './support/fixtures.ts'
import { startHub } from './support/hub.ts'

// SW 离线路径（D4b 遗留）：屏幕装好 Service Worker 并存下最后一份 snapshot 后，hub 整个停掉，
// 重新加载页面仍应从缓存的外壳与快照恢复出网格，并显示断线角标。自起一个 hub 以便真的把它关掉，不动共享 hub。
test('hub 停掉后重新加载屏幕：外壳与最后一份快照照常显示，带断线角标', async ({ browser }) => {
  test.setTimeout(90_000)
  const hub = await startHub()
  const context = await browser.newContext({ baseURL: hub.url, locale: 'zh-CN', viewport: { width: 1024, height: 600 } })
  try {
    // 没有管理员时屏幕只显示设置码页，先完成首次设置
    await ensureAdminSetup(context.request, hub.url, hub.setupCode)
    const page = await context.newPage()
    const token = readFileSync(path.join(hub.dataDir, 'screen.token'), 'utf8').trim()
    await page.goto(`/screen/auth?token=${encodeURIComponent(token)}`)
    await expect(page.locator('[data-screen-grid]')).toBeVisible()
    await page.evaluate(() => navigator.serviceWorker.ready.then(() => true))
    // 再加载一次：这次导航由 Service Worker 经手，外壳与资源进缓存
    await page.reload()
    await expect(page.locator('[data-screen-grid]')).toBeVisible()
    await expect(page.locator('[data-widget-id]').first()).toBeVisible()
    const widgets = await page.locator('section[data-widget-id]').count()
    // 快照按 60 秒节流落盘，页面隐藏与 pagehide 时补写一次：主动触发
    await page.evaluate(() => window.dispatchEvent(new Event('pagehide')))
    // 确定性等待：轮询存档缓存里出现快照条目，再关 hub
    await expect
      .poll(
        () =>
          page.evaluate(async () => {
            const cache = await caches.open('pimon-screen-data-v1')
            return (await cache.keys()).length
          }),
        { timeout: 15_000 },
      )
      .toBeGreaterThan(0)

    await hub.dispose()
    await page.reload()
    await expect(page.locator('[data-screen-grid]')).toBeVisible({ timeout: 15_000 })
    expect(await page.locator('section[data-widget-id]').count()).toBe(widgets)
    await expect(page.locator('[data-disconnect-badge]')).toBeVisible({ timeout: 15_000 })
  } finally {
    await context.close()
    await hub.dispose()
  }
})
