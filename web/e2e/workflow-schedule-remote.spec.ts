import { readFileSync } from 'node:fs'
import path from 'node:path'
import { adminPassword, ensureAdminSetup } from './support/admin.ts'
import { collectConsoleErrors, findOverflow } from './support/checks.ts'
import { expect, test } from './support/fixtures.ts'

// 管理员由 ensureAdminSetup 保证，可单独运行；用例结束前把时段计划与降低特效还原。

interface Schedule {
  periods: { start: string; end: string; theme: string }[]
}
interface ScreenStatus {
  state: { mode: string; reason: string }
}
interface Op {
  action: string
}

const screenToken = () => readFileSync(path.join(process.env.PIMON_E2E_DATA_DIR!, 'screen.token'), 'utf8').trim()

test('时段计划页与远程操作页：编辑联动、拆分、主题卡、降低特效保存；远程开关屏、操作记录与令牌重置二次确认', async ({ browser, baseURL }) => {
  const context = await browser.newContext({ baseURL, locale: 'zh-CN', viewport: { width: 1440, height: 900 } })
  try {
    await ensureAdminSetup(context.request, baseURL!)
    const login = await context.request.post('/api/login', { data: { password: adminPassword }, headers: { Origin: baseURL! } })
    expect(login.ok(), `API 登录失败：${login.status()}`).toBe(true)
    const getJson = async <T>(url: string) => (await (await context.request.get(url)).json()) as T
    const page = await context.newPage()
    const errors = collectConsoleErrors(page)
    const original = await getJson<Schedule>('/api/schedule')
    const originalReduce = (await getJson<{ reduce_effects: boolean }>('/api/settings')).reduce_effects

    // 时段计划：添加时段拆分所在段，改开始时间后列表与时间轴联动，主题卡赋给选中的时段
    await page.goto('/screens/schedule')
    await expect(page.getByTestId('timeline')).toBeVisible()
    const rows = page.locator('[data-testid^="period-row-"]')
    const startRows = await rows.count()
    await rows.first().click()
    await page.getByRole('button', { name: '添加时段' }).click()
    await expect(rows).toHaveCount(startRows + 1)
    await expect(page.getByTestId('problems')).toHaveCount(0)
    const startInput = page.getByRole('textbox', { name: '开始时间 HH:MM' })
    await startInput.fill('18:00')
    await expect(rows.nth(1)).toHaveAttribute('data-range', /^18:00-/)
    await expect(page.getByTestId('timeline').locator('[data-period="1"]').first()).toHaveAttribute('aria-label', /^18:00–/)
    await page.getByRole('button', { name: /（industrial）/ }).click()
    await expect(page.getByRole('button', { name: /（industrial）/ })).toHaveAttribute('aria-pressed', 'true')
    await page.getByRole('switch', { name: '降低特效' }).click()
    await page.getByRole('button', { name: '保存并推送' }).click()
    await expect(page.getByTestId('schedule-dirty-bar')).toHaveCount(0)
    const saved = await getJson<Schedule>('/api/schedule')
    expect(saved.periods.length).toBe(startRows + 1)
    expect(saved.periods.some((p) => p.start === '18:00' && p.theme === 'industrial')).toBe(true)
    expect((await getJson<{ reduce_effects: boolean }>('/api/settings')).reduce_effects).toBe(!originalReduce)

    // 窄屏与中等宽度不越界
    for (const width of [1024, 390]) {
      await page.setViewportSize({ width, height: 900 })
      await page.reload()
      await expect(page.getByTestId('timeline')).toBeVisible()
      expect(await findOverflow(page), `schedule ${width}`).toEqual([])
    }
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.reload()
    await expect(page.getByTestId('timeline')).toBeVisible()

    // 还原：删除新增的时段，把降低特效改回去
    await page.getByRole('button', { name: new RegExp(`^删除时段 ${startRows + 1}$`) }).click()
    await page.getByRole('switch', { name: '降低特效' }).click()
    await page.getByRole('button', { name: '保存并推送' }).click()
    await expect(page.getByTestId('schedule-dirty-bar')).toHaveCount(0)
    expect(await getJson<Schedule>('/api/schedule')).toEqual(original)
    expect((await getJson<{ reduce_effects: boolean }>('/api/settings')).reduce_effects).toBe(originalReduce)

    // 远程操作：关屏、开屏写入操作记录；状态卡与服务端一致
    await page.goto('/screens/remote')
    await expect(page.getByTestId('remote-state')).toBeVisible()
    await page.getByTestId('key-k4').click()
    await expect.poll(async () => (await getJson<ScreenStatus>('/api/screen/status')).state.mode).toBe('off')
    await expect(page.getByTestId('remote-state').getByText('已关屏')).toBeVisible()
    await expect(page.getByTestId('remote-state')).toContainText('下一个时段边界')
    await page.getByTestId('key-k3').click()
    await expect.poll(async () => (await getJson<ScreenStatus>('/api/screen/status')).state.mode).toBe('on')
    await expect(page.getByTestId('remote-log').locator('tr').first()).toContainText('开屏')
    const ops = await getJson<Op[]>('/api/screen/ops?limit=5')
    expect(ops.slice(0, 2).map((o) => o.action)).toEqual(['on', 'off'])

    // k6 二次确认：取消不重置；确认后令牌文件内容变化，并说明获取方式
    const before = screenToken()
    await page.getByTestId('key-k6').click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('旧令牌立即失效')
    await dialog.getByRole('button', { name: '取消' }).click()
    await expect(dialog).toHaveCount(0)
    expect(screenToken()).toBe(before)
    await page.getByTestId('key-k6').click()
    await page.getByRole('alertdialog').getByRole('button', { name: '重置令牌' }).click()
    await expect(page.getByTestId('token-done')).toContainText('screen.token')
    expect(screenToken()).not.toBe(before)
    await page.getByRole('alertdialog').getByRole('button', { name: '完成' }).click()
    await expect(page.getByTestId('remote-log').locator('tr').first()).toContainText('重置屏幕令牌')

    for (const width of [1024, 390]) {
      await page.setViewportSize({ width, height: 900 })
      await page.reload()
      await expect(page.getByTestId('remote-state')).toBeVisible()
      expect(await findOverflow(page), `remote ${width}`).toEqual([])
    }

    expect(errors).toEqual([])
  } finally {
    await context.close()
  }
})
