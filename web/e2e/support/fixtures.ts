import { test as base } from '@playwright/test'

// 地址由 globalSetup 启动的 hub 决定（随机端口），经环境变量传入
export const test = base.extend({
  // 固定夹具要求第一个参数是解构，这里借 browserName 占位（无实际依赖）
  baseURL: async ({ browserName }, provide) => {
    void browserName
    const url = process.env.PIMON_E2E_URL
    if (!url) throw new Error('缺少 PIMON_E2E_URL：hub 应由 globalSetup 启动')
    await provide(url)
  },
})

export { expect } from '@playwright/test'

export function setupCode(): string {
  const code = process.env.PIMON_E2E_SETUP_CODE
  if (!code) throw new Error('缺少 PIMON_E2E_SETUP_CODE：hub 应由 globalSetup 启动')
  return code
}
