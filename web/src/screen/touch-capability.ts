import { useCallback, useSyncExternalStore } from 'react'

const coarseQuery = '(any-pointer: coarse)'

/** 设置里的输入方式换算成「是否按有触摸处理」：auto 看 any-pointer: coarse */
export function hasTouch(inputMode: string): boolean {
  if (inputMode === 'touch') return true
  if (inputMode === 'none') return false
  return typeof matchMedia === 'function' && matchMedia(coarseQuery).matches
}

export function useHasTouch(inputMode: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      if (inputMode !== 'auto' || typeof matchMedia !== 'function') return () => {}
      const mq = matchMedia(coarseQuery)
      mq.addEventListener('change', onChange)
      return () => mq.removeEventListener('change', onChange)
    },
    [inputMode],
  )
  return useSyncExternalStore(subscribe, () => hasTouch(inputMode))
}
