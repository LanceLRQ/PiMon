// 轮播顺序与停留时长的纯函数；计时与优先级在 state-machine.ts。

export interface RotationScreen {
  id: string
  /** 0 表示用全局默认停留秒数 */
  dwellSeconds: number
  inRotation: boolean
}

/** 每屏停留毫秒数：单独设置优先，否则用全局默认 */
export function dwellMs(screen: RotationScreen, defaultDwellSeconds: number): number {
  return (screen.dwellSeconds > 0 ? screen.dwellSeconds : defaultDwellSeconds) * 1000
}

/**
 * 轮播的下一个 screen：按布局顺序，从当前位置往后找第一个参与轮播的，末尾回绕。
 * 当前 screen 自己不算；没有可去的（无人参与或只有当前一个）返回 null。
 * 当前 screen 已不存在时从头开始找。
 */
export function nextInRotation(screens: readonly RotationScreen[], currentId: string): string | null {
  const n = screens.length
  if (n === 0) return null
  const at = screens.findIndex((s) => s.id === currentId)
  for (let step = 1; step <= n; step++) {
    const s = screens[(at + step + n) % n]
    if (at >= 0 && s.id === currentId) continue
    if (s.inRotation) return s.id
  }
  return null
}
