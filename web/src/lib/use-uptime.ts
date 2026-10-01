import { useEffect, useState } from 'react'

// 以接口返回的 uptime_seconds 为准，加上取回之后本机经过的时间；
// 不用浏览器时间减启动时刻，避免两端时钟偏差。取不到（undefined）时返回 null，调用方显示「未知」。
export function useUptimeSeconds(uptimeSeconds: number | null | undefined, tickMs = 1000): number | null {
  const [anchor, setAnchor] = useState<{ value: number; at: number } | null>(null)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 每次取回新的运行时长就重置基准
    setAnchor(typeof uptimeSeconds === 'number' && Number.isFinite(uptimeSeconds) ? { value: uptimeSeconds, at: Date.now() } : null)
  }, [uptimeSeconds])

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), tickMs)
    return () => clearInterval(id)
  }, [tickMs])

  return anchor ? anchor.value + Math.max(0, now - anchor.at) / 1000 : null
}
