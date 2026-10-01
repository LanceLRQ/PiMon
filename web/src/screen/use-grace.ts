import { useEffect, useState } from 'react'

/** 条件持续成立满 ms 毫秒后才返回 true；条件一旦不成立立刻回到 false。用来避免页面刚加载、连接尚未建立时角标闪现。 */
export function useGrace(active: boolean, ms: number): boolean {
  const [elapsed, setElapsed] = useState(false)
  useEffect(() => {
    if (!active) return
    const t = setTimeout(() => setElapsed(true), ms)
    return () => {
      clearTimeout(t)
      setElapsed(false)
    }
  }, [active, ms])
  return active && elapsed
}
