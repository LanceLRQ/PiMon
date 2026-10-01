import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import path from 'node:path'

export interface RunningHub {
  url: string
  setupCode: string
  dataDir: string
  workDir: string
  child: ChildProcess
  // 关闭 hub（整个进程组）、删除临时目录并撤销兜底钩子；可重复调用
  dispose: () => Promise<void>
}

// hub 只需要这几个环境变量，不继承运行器的完整环境（避免带入密钥等无关变量）
const hubEnvAllowlist = ['PATH', 'HOME', 'TMPDIR', 'LANG', 'LC_ALL', 'TZ']

function hubEnv(): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {}
  for (const k of hubEnvAllowlist) if (process.env[k] !== undefined) env[k] = process.env[k]
  return env
}

// 杀掉 hub 所在进程组（hub 以 detached 启动，自成一组）
function killGroup(child: ChildProcess | undefined, signal: NodeJS.Signals) {
  if (!child?.pid) return
  try {
    process.kill(-child.pid, signal)
  } catch {
    // 进程已退出
  }
}

// 向系统要一个空闲端口：先监听 0 号端口取得分配结果再释放
export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer()
    srv.once('error', reject)
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address()
      srv.close(() => (addr && typeof addr === 'object' ? resolve(addr.port) : reject(new Error('无法取得空闲端口'))))
    })
  })
}

// 编译 hub；PIMON_E2E_HUB_BIN 指向现成二进制时跳过。前端必须已构建（make web），否则 embed 的是旧产物
function ensureBinary(workDir: string): string {
  const prebuilt = process.env.PIMON_E2E_HUB_BIN
  if (prebuilt) return prebuilt
  const out = path.join(workDir, 'pimon-hub')
  const res = spawnSync('go', ['build', '-o', out, './cmd/pimon-hub'], {
    cwd: path.resolve(import.meta.dirname, '../../../src'),
    env: { ...process.env, CGO_ENABLED: '0' },
    encoding: 'utf8',
  })
  if (res.status !== 0) throw new Error(`编译 pimon-hub 失败：\n${res.stderr}${res.stdout}`)
  return out
}

// 在临时数据目录与随机端口启动真实 hub，从 stderr 取首次设置码，等 /healthz 就绪后返回。
// 兜底清理在一开始（含 go build 阶段）就挂好：进程退出、SIGTERM、SIGINT 时杀进程组并删临时目录。
export async function startHub(): Promise<RunningHub> {
  const workDir = mkdtempSync(path.join(tmpdir(), 'pimon-e2e-'))
  const dataDir = path.join(workDir, 'data')
  mkdirSync(dataDir)
  let child: ChildProcess | undefined

  const cleanupNow = () => {
    killGroup(child, 'SIGKILL')
    rmSync(workDir, { recursive: true, force: true })
  }
  const onSignal = (signal: NodeJS.Signals) => {
    cleanupNow()
    detachHooks()
    // 我们的监听会让默认的终止行为失效：SIGTERM 清理后按默认方式终止；SIGINT 交给运行器自己的处理继续走 teardown
    if (signal === 'SIGTERM') process.kill(process.pid, 'SIGTERM')
  }
  const onTerm = () => onSignal('SIGTERM')
  const onInt = () => onSignal('SIGINT')
  function detachHooks() {
    process.removeListener('exit', cleanupNow)
    process.removeListener('SIGTERM', onTerm)
    process.removeListener('SIGINT', onInt)
  }
  process.on('exit', cleanupNow)
  process.on('SIGTERM', onTerm)
  process.on('SIGINT', onInt)

  const dispose = async () => {
    detachHooks()
    if (child && child.exitCode === null && child.signalCode === null) {
      await new Promise<void>((resolve) => {
        const timer = setTimeout(() => killGroup(child, 'SIGKILL'), 8000)
        child!.once('exit', () => {
          clearTimeout(timer)
          resolve()
        })
        killGroup(child, 'SIGTERM')
      })
    }
    killGroup(child, 'SIGKILL')
    rmSync(workDir, { recursive: true, force: true })
  }

  try {
    const bin = ensureBinary(workDir)
    const port = await freePort()
    const addr = `127.0.0.1:${port}`
    child = spawn(bin, ['serve', '--addr', addr, '--data-dir', dataDir], {
      stdio: ['ignore', 'pipe', 'pipe'],
      detached: true,
      env: hubEnv(),
    })
    let output = ''
    let exited = false
    child.once('exit', () => {
      exited = true
    })
    const collect = (b: Buffer) => {
      output += b.toString()
    }
    child.stdout?.on('data', collect)
    child.stderr?.on('data', collect)

    const url = `http://${addr}`
    const deadline = Date.now() + 30_000
    let setupCode = ''
    for (;;) {
      if (exited) throw new Error(`hub 提前退出：\n${output}`)
      if (Date.now() > deadline) throw new Error(`等待 hub 就绪超时：\n${output}`)
      // 按完整行匹配（以「）」结尾并已换行），避免管道截断时取到不完整的码
      const m = output.match(/^首次设置码: ([A-Za-z0-9-]+)（[^\n]*）\r?\n/m)
      if (m) setupCode = m[1]
      if (setupCode) {
        try {
          if ((await fetch(`${url}/healthz`)).ok) break
        } catch {
          // 尚未监听，继续等
        }
      }
      await new Promise((r) => setTimeout(r, 100))
    }
    return { url, setupCode, dataDir, workDir, child, dispose }
  } catch (err) {
    detachHooks()
    cleanupNow()
    throw err
  }
}
