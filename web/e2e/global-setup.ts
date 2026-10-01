import { startHub } from './support/hub.ts'

// 启动真实 hub，把地址与设置码经环境变量交给 worker；返回的函数即 globalTeardown。
// 异常退出与信号的兜底清理在 startHub 内部。
export default async function globalSetup() {
  const hub = await startHub()
  process.env.PIMON_E2E_URL = hub.url
  process.env.PIMON_E2E_SETUP_CODE = hub.setupCode
  return hub.dispose
}
