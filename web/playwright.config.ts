import { defineConfig } from '@playwright/test'

// 冒烟用例对着真实 hub 运行；hub 的启动脚本与测试数据准备在 C9 提供，
// 这里只约定基础地址，可用 PIMON_E2E_URL 覆盖
export default defineConfig({
  testDir: './e2e',
  reporter: 'list',
  use: {
    baseURL: process.env.PIMON_E2E_URL ?? 'http://127.0.0.1:31415',
  },
})
