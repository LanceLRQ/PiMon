import { useEffect, useState, useSyncExternalStore } from 'react'

function subscribe(onChange: () => void) {
  window.addEventListener('resize', onChange)
  return () => window.removeEventListener('resize', onChange)
}

/** 窗口内尺寸（CSS 像素），随 resize 立即更新；网格重排不防抖，上报才防抖 */
export function useViewportSize(): { width: number; height: number } {
  const width = useSyncExternalStore(subscribe, () => window.innerWidth)
  const height = useSyncExternalStore(subscribe, () => window.innerHeight)
  return { width, height }
}

/** 唤醒后冻结视口的窗口（设计 5.5a：关屏期间及唤醒后 5 秒内的 viewport 变化不重算网格） */
export const WAKE_FREEZE_MS = 5000

/**
 * 屏幕由关转开后 5 秒内沿用唤醒前的尺寸，到期再读取最新尺寸；平时直接跟随窗口。
 * 关屏期间的窗口变化同样不采用（关屏时网格也未渲染）。
 */
export function useWakeFrozenSize(live: { width: number; height: number }, mode: 'on' | 'off') {
  const [settled, setSettled] = useState(live)
  const [prevMode, setPrevMode] = useState(mode)
  const [frozen, setFrozen] = useState(false)
  // 在渲染期间依据上一次的模式调整状态（React 推荐的「由 props 派生状态」写法）
  if (mode !== prevMode) {
    setPrevMode(mode)
    if (mode === 'on') setFrozen(true)
  }
  if (mode === 'on' && !frozen && (settled.width !== live.width || settled.height !== live.height)) {
    setSettled(live)
  }
  useEffect(() => {
    if (!frozen) return
    const t = setTimeout(() => setFrozen(false), WAKE_FREEZE_MS)
    return () => clearTimeout(t)
  }, [frozen])
  return frozen || mode === 'off' ? settled : live
}
