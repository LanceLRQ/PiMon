import type { APIRequestContext } from '@playwright/test'
import { setupCode } from './fixtures.ts'

// 端到端用例共用的管理员密码（smoke 走页面完成首次设置时也用它）
export const adminPassword = 'E2e-smoke-pass-1'

// 尚未完成首次设置就用设置码走接口设置；已设置则什么也不做。让依赖管理员的用例能单独运行。
// code 缺省取全局 hub 的设置码；矩阵用例自起 hub 时传入该 hub 自己的设置码。
export async function ensureAdminSetup(request: APIRequestContext, baseURL: string, code: string = setupCode()): Promise<void> {
  const status = await request.get('/api/setup/status')
  const { needs_setup: needs } = (await status.json()) as { needs_setup: boolean }
  if (!needs) return
  const res = await request.post('/api/setup', {
    data: { setup_code: code, password: adminPassword },
    headers: { Origin: baseURL },
  })
  if (!res.ok()) throw new Error(`首次设置失败：${res.status()} ${await res.text()}`)
}
