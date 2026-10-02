import path from 'node:path'
import { startHub } from './support/hub.ts'

// 启动真实 hub，把地址与设置码经环境变量交给 worker；返回的函数即 globalTeardown。
// 异常退出与信号的兜底清理在 startHub 内部。
export default async function globalSetup() {
  const hub = await startHub()
  process.env.PIMON_E2E_URL = hub.url
  process.env.PIMON_E2E_SETUP_CODE = hub.setupCode
  process.env.PIMON_E2E_DATA_DIR = hub.dataDir
  // 主题矩阵为每种屏幕自起一个干净的 hub，复用这里编译好的二进制，省去重复编译
  process.env.PIMON_E2E_HUB_BIN ??= path.join(hub.workDir, 'pimon-hub')
  return hub.dispose
}
