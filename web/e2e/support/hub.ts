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

// 在临时数据目录与随机端口启动真实 hub，从 stderr 取首次设置码，等 /healthz 就绪后返回
export async function startHub(): Promise<RunningHub> {
  const workDir = mkdtempSync(path.join(tmpdir(), 'pimon-e2e-'))
  const dataDir = path.join(workDir, 'data')
  mkdirSync(dataDir)
  let child: ChildProcess | undefined
  try {
    const bin = ensureBinary(workDir)
    const port = await freePort()
    const addr = `127.0.0.1:${port}`
    child = spawn(bin, ['serve', '--addr', addr, '--data-dir', dataDir], { stdio: ['ignore', 'pipe', 'pipe'] })
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
      const m = output.match(/首次设置码:\s*([A-Za-z0-9-]+)/)
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
    return { url, setupCode, dataDir, workDir, child }
  } catch (err) {
    child?.kill('SIGKILL')
    rmSync(workDir, { recursive: true, force: true })
    throw err
  }
}

// 关闭 hub 并删除临时目录；重复调用安全
export async function stopHub(hub: Pick<RunningHub, 'child' | 'workDir'>): Promise<void> {
  const { child } = hub
  if (child.exitCode === null && child.signalCode === null) {
    await new Promise<void>((resolve) => {
      const timer = setTimeout(() => child.kill('SIGKILL'), 8000)
      child.once('exit', () => {
        clearTimeout(timer)
        resolve()
      })
      child.kill('SIGTERM')
    })
  }
  rmSync(hub.workDir, { recursive: true, force: true })
}
