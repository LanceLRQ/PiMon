import { expect, test } from '@playwright/test'

test('首页能加载出前端页面', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'PiMon' })).toBeVisible()
})
