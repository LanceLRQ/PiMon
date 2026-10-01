import { useSyncExternalStore } from 'react'

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
