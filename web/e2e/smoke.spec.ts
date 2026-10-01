import type { Browser, BrowserContext, Page } from '@playwright/test'
import { collectConsoleErrors, findOverflow } from './support/checks.ts'
import { expect, setupCode, test } from './support/fixtures.ts'

const adminPassword = 'E2e-smoke-pass-1'
const sizes = [
  { name: '桌面 1440×900', width: 1440, height: 900 },
  { name: '手机 390×844', width: 390, height: 844 },
] as const
const themes = ['light', 'dark'] as const
const combos = sizes.flatMap((size) => themes.map((theme) => ({ size, theme, label: `${size.name} · ${theme === 'light' ? '浅色' : '深色'}` })))
type Combo = (typeof combos)[number]

// 新建浏览器上下文：指定尺寸、中文界面，并在页面脚本运行前预置管理界面主题
async function openContext(browser: Browser, baseURL: string, combo: Combo): Promise<BrowserContext> {
  const context = await browser.newContext({
    baseURL,
    viewport: { width: combo.size.width, height: combo.size.height },
    locale: 'zh-CN',
  })
  await context.addInitScript((theme) => {
    try {
      localStorage.setItem('pimon.admin.theme', theme)
    } catch {
      // 存储不可用时按默认主题运行
    }
  }, combo.theme)
  return context
}

// 写接口要校验 Origin，经 API 登录时手动带上
function originHeaders(baseURL: string) {
  return { Origin: baseURL }
}

async function apiLogin(context: BrowserContext, baseURL: string) {
  const res = await context.request.post('/api/login', { data: { password: adminPassword }, headers: originHeaders(baseURL) })
  expect(res.ok(), `API 登录失败：${res.status()}`).toBe(true)
}

// 页面稳定后断言：无横向滚动、容器内元素不溢出
async function expectLayoutSound(page: Page, where: string) {
  const offenders = await findOverflow(page)
  expect(offenders, `${where} 存在溢出：\n${offenders.map((o) => `  [${o.kind}] ${o.detail}`).join('\n')}`).toEqual([])
}

async function fillSetupCode(page: Page) {
  const groups = setupCode().split('-')
  for (let i = 0; i < groups.length; i++) await page.getByLabel(`第 ${i + 1} 组`).fill(groups[i])
}

test.describe.configure({ mode: 'serial' })

test.describe('首次设置页（未提交，逐尺寸逐主题）', () => {
  for (const combo of combos) {
    test(combo.label, async ({ browser, baseURL }) => {
      const context = await openContext(browser, baseURL!, combo)
      const page = await context.newPage()
      const errors = collectConsoleErrors(page)
      await page.goto('/')
      await expect(page).toHaveURL(/\/setup$/)
      await expect(page.getByLabel('第 1 组')).toBeVisible()
      await expectLayoutSound(page, '首次设置 · 设置码')

      await fillSetupCode(page)
      await page.getByRole('button', { name: '下一步' }).click()
      await page.locator('#setup-password').fill(adminPassword)
      await page.locator('#setup-confirm').fill(adminPassword)
      await expectLayoutSound(page, '首次设置 · 管理员密码')

      await page.getByRole('button', { name: '下一步' }).click()
      await expect(page.getByRole('button', { name: '完成设置' })).toBeVisible()
      await expectLayoutSound(page, '首次设置 · 基础设置')

      expect(errors).toEqual([])
      await context.close()
    })
  }
})

test.describe('主路径', () => {
  let context: BrowserContext
  let page: Page
  let errors: string[]

  test.beforeAll(async ({ browser, baseURL }) => {
    context = await openContext(browser, baseURL!, combos[0])
    page = await context.newPage()
    errors = collectConsoleErrors(page)
  })
  test.afterAll(async () => {
    await context.close()
  })

  test('首次设置：设置码 → 密码 → 基础设置 → 完成', async () => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/setup$/)
    await fillSetupCode(page)
    await page.getByRole('button', { name: '下一步' }).click()
    await page.locator('#setup-password').fill(adminPassword)
    await page.locator('#setup-confirm').fill(adminPassword)
    await page.getByRole('button', { name: '下一步' }).click()
    await page.getByRole('button', { name: '完成设置' }).click()
    await expect(page.getByRole('heading', { name: '设置完成' })).toBeVisible()
    await expectLayoutSound(page, '首次设置 · 完成')
    await page.getByRole('button', { name: '进入总览' }).click()
    // 首次设置接口会同时签发会话，进入总览后应已登录
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByRole('complementary', { name: '主导航' })).toBeVisible()
  })

  test('经 API 建一个 demo 实例，总览能看到它', async ({ baseURL }) => {
    const res = await context.request.post('/api/instances', {
      data: { plugin_id: 'demo', name: '演示数据', config: {}, interval_seconds: 0 },
      headers: originHeaders(baseURL!),
    })
    expect(res.status(), await res.text()).toBe(201)
    await page.goto('/')
    await expect(page.getByText('演示数据').first()).toBeVisible()
  })

  test('新建 http-check（目标为 hub 自己的 /healthz）并保存测试', async ({ baseURL }) => {
    await page.goto('/instances/new')
    await page.getByRole('button', { name: /HTTP 检测/ }).click()
    await page.getByLabel('实例名称').fill('本机健康检查')
    await page.getByLabel('地址', { exact: false }).first().fill(`${baseURL}/healthz`)
    await page.getByRole('button', { name: '保存并测试' }).click()
    // 保存成功后跳到编辑页，并自动运行一次测试；结果里应出现数据项
    await expect(page).toHaveURL(/\/instances\/[^/]+\/edit$/)
    await expect(page.getByText(/已完成测试|数据项（\d+）/).first()).toBeVisible()
    await page.goto('/instances')
    await expect(page.getByText('本机健康检查').first()).toBeVisible()
  })

  test('改设置：访问地址保存后刷新仍在', async () => {
    await page.goto('/settings')
    const url = page.locator('#set-url')
    await url.fill('https://pimon.e2e.test')
    await page.getByRole('button', { name: '保存', exact: true }).click()
    await expect(page.getByText('已保存设置').first()).toBeVisible()
    await page.reload()
    await expect(page.locator('#set-url')).toHaveValue('https://pimon.e2e.test')
  })

  test('登出后回到登录页，再用密码登录', async () => {
    await page.getByRole('button', { name: '退出登录' }).click()
    await expect(page).toHaveURL(/\/login/)
    await expectLayoutSound(page, '登录页')
    await page.getByLabel('管理员密码').fill(adminPassword)
    await page.getByRole('button', { name: '登录', exact: true }).click()
    // 登录后回到登出前所在的页面
    await expect(page).toHaveURL(/\/settings$/)
    await expect(page.getByRole('complementary', { name: '主导航' })).toBeVisible()
    await page.goto('/')
    await expect(page.getByText('演示数据').first()).toBeVisible()
  })

  test('主路径全程 console 零报错', () => {
    expect(errors).toEqual([])
  })
})

// 受保护页面：路径与用于确认页面渲染完成的文字
const adminPages = [
  { name: '总览', path: '/', ready: '本机健康检查' },
  { name: '实例列表', path: '/instances', ready: '实例列表' },
  { name: '新建实例', path: '/instances/new', ready: 'HTTP 检测' },
  { name: '代理', path: '/proxies', ready: '代理列表' },
  { name: '设置', path: '/settings', ready: '通用' },
  { name: '系统', path: '/system', ready: '插件' },
] as const

test.describe('逐页布局（登录与各管理页，逐尺寸逐主题）', () => {
  for (const combo of combos) {
    test(combo.label, async ({ browser, baseURL }) => {
      const context = await openContext(browser, baseURL!, combo)
      const page = await context.newPage()
      const errors = collectConsoleErrors(page)

      await page.goto('/login')
      await expect(page.getByLabel('管理员密码')).toBeVisible()
      await expectLayoutSound(page, '登录页')

      await apiLogin(context, baseURL!)
      for (const p of adminPages) {
        await page.goto(p.path)
        await expect(page.locator('main').getByText(p.ready, { exact: true }).first(), `${p.name} 未渲染完成`).toBeVisible()
        // 等实时数据与懒加载内容落定后再量
        await page.waitForLoadState('networkidle')
        await expectLayoutSound(page, p.name)
      }

      // 打开抽屉与编辑页（带最近一次测试结果）后的状态
      await page.goto('/instances')
      await page.getByText('本机健康检查').first().click()
      await expect(page.getByRole('dialog')).toBeVisible()
      await page.waitForLoadState('networkidle')
      await expectLayoutSound(page, '实例详情抽屉')
      await page.keyboard.press('Escape')

      await page.goto('/proxies')
      await page.getByRole('button', { name: '添加代理' }).first().click()
      await expect(page.getByRole('dialog')).toBeVisible()
      await expectLayoutSound(page, '添加代理抽屉')
      await page.keyboard.press('Escape')

      const list = (await (await context.request.get('/api/instances')).json()) as { id: string; name: string }[]
      const check = list.find((i) => i.name === '本机健康检查')
      expect(check).toBeTruthy()
      await page.goto(`/instances/${check!.id}/edit`)
      await expect(page.getByRole('button', { name: '保存并测试' })).toBeVisible()
      await page.getByRole('button', { name: '保存并测试' }).click()
      await expect(page.getByText(/数据项（\d+）/).first()).toBeVisible()
      await expectLayoutSound(page, '编辑实例（含测试结果）')

      expect(errors).toEqual([])
      await context.close()
    })
  }
})
