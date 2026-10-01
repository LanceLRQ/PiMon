import { useSyncExternalStore } from 'react'

const query = '(max-width: 640px)'

function subscribe(cb: () => void): () => void {
  if (typeof matchMedia !== 'function') return () => {}
  const mq = matchMedia(query)
  mq.addEventListener('change', cb)
  return () => mq.removeEventListener('change', cb)
}

// 手机宽度（≤640px，与 mobile: 变体一致）；没有 matchMedia 的环境按桌面处理
export function useIsMobile(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => typeof matchMedia === 'function' && matchMedia(query).matches,
    () => false,
  )
}
