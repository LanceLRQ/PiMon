import { defineConfig } from '@playwright/test'

// 冒烟用例对着真实 hub 运行：globalSetup 在临时数据目录与随机端口启动 hub，
// 结束时（含失败）关闭进程并删除临时目录。前端需先构建（make web）再运行。
export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.ts',
  reporter: 'list',
  // 用例之间共享同一个 hub 的状态（首次设置 → 登录 → …），必须串行
  workers: 1,
  fullyParallel: false,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: 0,
  use: {
    // 用例里的上下文都是手动创建的（含 beforeAll 里的），trace 与失败截图由 smoke.spec.ts 自己保存
    trace: 'off',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
})
