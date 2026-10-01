import { useEffect, useState } from 'react'
import { isApiError, type ApiError } from '@/api/errors'
import { serverNow } from '@/store/live-store'

// auth.locked 的 details（src/internal/hub/httpx/errors.go）
export interface LockInfo {
  // 响应到达时刻起的剩余毫秒，不依赖浏览器与中枢的时钟是否一致
  remainingMs: number
  // 锁定到期时刻（ms，取自 locked_until），仅用于显示；缺失时由剩余时间推算
  untilMs: number
  clientIp: string | null
}

// 从 auth.locked 错误取出锁定信息；其他错误返回 null
export function lockInfoOf(err: unknown, now: number = Date.now()): LockInfo | null {
  if (!isApiError(err) || err.code !== 'auth.locked') return null
  return lockFromDetails(err, now)
}

function lockFromDetails(err: ApiError, now: number): LockInfo {
  const d = err.details
  const retry = typeof d.retry_after_seconds === 'number' ? d.retry_after_seconds : null
  const until = typeof d.locked_until === 'string' ? Date.parse(d.locked_until) : NaN
  const remainingMs = retry !== null ? retry * 1000 : Number.isNaN(until) ? 0 : Math.max(0, until - now)
  return {
    remainingMs,
    untilMs: Number.isNaN(until) ? now + remainingMs : until,
    clientIp: typeof d.client_ip === 'string' && d.client_ip !== '' ? d.client_ip : null,
  }
}

export interface Lock {
  info: LockInfo
  // 本地截止时刻（按注入的时钟）
  deadline: number
}

export function makeLock(info: LockInfo, now: number): Lock {
  return { info, deadline: now + info.remainingMs }
}

// 倒计时：返回剩余毫秒；到 0 时调用 onExpire。时钟可注入，默认取校正后的服务端时间
export function useCountdown(lock: Lock | null, onExpire: () => void, now: () => number = serverNow): number {
  const [remaining, setRemaining] = useState(() => (lock ? Math.max(0, lock.deadline - now()) : 0))
  useEffect(() => {
    if (!lock) return
    const tick = () => {
      const left = Math.max(0, lock.deadline - now())
      setRemaining(left)
      if (left <= 0) {
        clearInterval(id)
        onExpire()
      }
    }
    const id = setInterval(tick, 1000)
    tick()
    return () => clearInterval(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lock])
  return lock ? remaining : 0
}

// 剩余时间 mm:ss
export function formatRemaining(ms: number): string {
  const total = Math.ceil(ms / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

// 到期时刻 HH:mm（浏览器本地时区）
export function formatClockTime(ms: number, locale: string): string {
  return new Date(ms).toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', hour12: false })
}
