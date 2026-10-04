import { DatabaseSync } from 'node:sqlite'

// 往 hub 的 devices 表直接造设备行（没有不带真 agent 就能创建设备的接口）。
// 在线状态是 hub 的运行时状态，不落库，由行字段与启动时机推导（见 src/internal/hub/devices/view.go、presence.go）：
//   - 启动时已在库里、已确认的设备：先是 pending，hub 起满 90 秒仍未连上改为 offline；
//     allow_offline = 1 的 offline 对外显示 dormant（休眠）；
//   - hub 运行中才插入的已确认设备：没有在线记录，一律按 pending；
//   - maintenance = 1：灯组格为维护中（优先于在线状态）；
//   - incompatible_reason 非空且不在线：灯组格为「需要升级」；
//   - confirmed_at 为空的拨入占位：不进灯组（15 分钟后会被清理，created_at 取当前时间即可）。
// 灯组顺序按 rowid，即插入顺序。

export interface SeedDevice {
  name: string
  role?: 'server' | 'nas' | 'dev' | 'vps'
  allowOffline?: boolean
  maintenance?: boolean
  incompatibleReason?: string
  // 为 true 时写成未确认的拨入占位（confirmed_at 为空）
  unconfirmed?: boolean
}

export const longDeviceName = '超长设备名称-extremely-long-device-name-that-must-be-truncated-0123456789-零一二三四五六七八九'

// 极端数据页用的 20 台设备：部分名称超长，含休眠、维护中、需要升级，外加一个未确认占位（不计入 20 台，也不进灯组）
export function extremeDevices(): SeedDevice[] {
  const list: SeedDevice[] = []
  for (let i = 1; i <= 20; i++) {
    const d: SeedDevice = { name: i % 4 === 1 ? `${longDeviceName}-${i}` : `设备${String(i).padStart(2, '0')}`, role: 'server' }
    if (i % 3 === 0) d.allowOffline = true
    if (i === 5) d.maintenance = true
    if (i === 7) d.incompatibleReason = 'protocol_too_old'
    list.push(d)
  }
  list.push({ name: '未确认占位', unconfirmed: true })
  return list
}

let counter = 0

export function insertDevices(dbPath: string, list: SeedDevice[]): void {
  const db = new DatabaseSync(dbPath)
  try {
    // hub 可能正在运行：等写锁而不是直接失败
    db.exec('PRAGMA busy_timeout = 10000')
    const stmt = db.prepare(`INSERT INTO devices
      (id, name, role, direction, allow_offline, maintenance, incompatible_reason, confirmed_at, created_at, updated_at)
      VALUES (?, ?, ?, 'dial_in', ?, ?, ?, ?, ?, ?)`)
    const now = new Date().toISOString()
    for (const d of list) {
      counter++
      const id = `e2e${Date.now().toString(16)}${counter.toString(16)}`.padEnd(16, '0').slice(0, 16)
      stmt.run(id, d.name, d.role ?? 'server', d.allowOffline ? 1 : 0, d.maintenance ? 1 : 0, d.incompatibleReason ?? '', d.unconfirmed ? null : now, now, now)
    }
  } finally {
    db.close()
  }
}
