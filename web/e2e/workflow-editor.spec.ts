import { readFileSync } from 'node:fs'
import path from 'node:path'
import { collectConsoleErrors } from './support/checks.ts'
import { expect, test } from './support/fixtures.ts'

// 依赖 smoke.spec.ts 先完成首次设置（管理员密码）；Playwright 按文件名顺序串行执行，本文件名排在 smoke 之后。
const adminPassword = 'E2e-smoke-pass-1'
// Ruling 21：布局推送不进合并窗口；验收按实测 ≤1 秒人工判定，断言放宽到 1.5 秒避免偶发抖动
const hotUpdateBudgetMs = 1500

function screenToken(): string {
  const dataDir = process.env.PIMON_E2E_DATA_DIR
  if (!dataDir) throw new Error('缺少 PIMON_E2E_DATA_DIR：hub 应由 globalSetup 启动')
  // 令牌可能被前面的用例轮换过，每次都读文件当前值
  return readFileSync(path.join(dataDir, 'screen.token'), 'utf8').trim()
}

test('布局编辑器主路径：拖入 → 调整 → 保存 → 另一个标签页里的 /screen 热更新，两边 console 零报错', async ({ browser, baseURL }, testInfo) => {
  const screenContext = await browser.newContext({ baseURL, locale: 'zh-CN', viewport: { width: 1024, height: 600 } })
  const adminContext = await browser.newContext({ baseURL, locale: 'zh-CN', viewport: { width: 1440, height: 900 } })
  try {
    // 屏幕标签页：先上线，让显示器视口稳定为 1024×600（8×5 网格）
    const screenPage = await screenContext.newPage()
    const screenErrors = collectConsoleErrors(screenPage)
    await screenPage.goto(`/screen/auth?token=${encodeURIComponent(screenToken())}`)
    await expect(screenPage.locator('[data-screen-grid]')).toBeVisible()
    const idsOnScreen = () => screenPage.locator('[data-widget-id]').evaluateAll((els) => [...new Set(els.map((e) => e.getAttribute('data-widget-id')))])
    const before = await idsOnScreen()
    expect(before.length).toBeGreaterThan(1)

    // 管理端标签页：登录并打开编辑器
    const login = await adminContext.request.post('/api/login', { data: { password: adminPassword }, headers: { Origin: baseURL! } })
    expect(login.ok(), `API 登录失败：${login.status()}`).toBe(true)
    const page = await adminContext.newPage()
    const errors = collectConsoleErrors(page)
    await page.goto('/screens/editor')
    const editor = page.getByTestId('layout-editor')
    await expect(editor).toBeVisible()
    await expect(page.getByTestId('dirty-chip')).toHaveAttribute('data-dirty', 'false')
    const baseText = await page.getByTestId('base-version').innerText()

    // 拖入：时钟拖到 8×5 种子布局右下角的空格（第 8 列第 5 行）
    const bezel = page.getByTestId('editor-bezel')
    await expect(bezel).toBeVisible()
    const box = (await bezel.boundingBox())!
    const cw = box.width / 8
    const ch = box.height / 5
    await page.getByTestId('lib-item-plugin:core:clock').dragTo(bezel, { targetPosition: { x: 7.5 * cw, y: 4.5 * ch } })
    const added = page.locator('[data-editor-widget][aria-pressed="true"]')
    await expect(added).toHaveCount(1)
    await expect(added).toHaveAttribute('aria-label', /第 8 列第 5 行/)
    await expect(page.getByTestId('dirty-chip')).toHaveText(/未保存 1 处/)

    // 调整：左移一格并改标题
    await added.focus()
    await page.keyboard.press('ArrowLeft')
    await expect(added).toHaveAttribute('aria-label', /第 7 列第 5 行/)
    await page.getByLabel('标题').fill('端到端时钟')
    await expect(page.getByTestId('changes-panel')).toContainText('新增')

    // 保存：从点击保存开始计时，屏幕标签页应在预算内多出一个小组件（同一小组件在屏幕 DOM 里有两个带 id 的节点，按 id 去重计数）
    const t0 = Date.now()
    const saved = page.waitForResponse((r) => r.url().endsWith('/api/screens') && r.request().method() === 'PUT')
    await page.getByRole('button', { name: '保存并推送' }).click()
    const res = await saved
    expect(res.status()).toBe(200)
    await screenPage.waitForFunction(
      (n) => new Set([...document.querySelectorAll('[data-widget-id]')].map((e) => e.getAttribute('data-widget-id'))).size === n,
      before.length + 1,
      { timeout: hotUpdateBudgetMs },
    )
    const elapsed = Date.now() - t0
    testInfo.annotations.push({ type: '热更新耗时', description: `${elapsed} ms（点击保存到屏幕多出新小组件）` })
    console.log(`布局热更新耗时 ${elapsed} ms`)
    const after = await idsOnScreen()
    expect(after.filter((id) => !before.includes(id))).toHaveLength(1)
    expect(await screenPage.locator('.tpl-clock').count()).toBeGreaterThanOrEqual(2)

    // 管理端：基线推进、清单清空
    await expect(page.getByTestId('base-version')).not.toHaveText(baseText)
    await expect(page.getByTestId('dirty-chip')).toHaveAttribute('data-dirty', 'false')

    // 版本历史里能看到新版本
    await page.getByRole('button', { name: '版本历史' }).click()
    await expect(page.getByTestId('history-list').getByRole('listitem').first()).toContainText('当前')
    await page.keyboard.press('Escape')

    expect(errors).toEqual([])
    expect(screenErrors).toEqual([])
  } finally {
    await adminContext.close()
    await screenContext.close()
  }
})
