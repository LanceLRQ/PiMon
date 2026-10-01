import { startHub, stopHub } from './support/hub.ts'

// 启动真实 hub，把地址与设置码经环境变量交给 worker；返回的函数即 globalTeardown
export default async function globalSetup() {
  const hub = await startHub()
  // 运行器被强杀时兜底杀掉 hub，避免遗留进程
  process.once('exit', () => hub.child.kill('SIGKILL'))
  process.env.PIMON_E2E_URL = hub.url
  process.env.PIMON_E2E_SETUP_CODE = hub.setupCode
  return async () => {
    await stopHub(hub)
  }
}
