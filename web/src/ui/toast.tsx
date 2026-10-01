import { AlertTriangle, Check } from 'lucide-react'
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

export type ToastTone = 'ok' | 'warn'

interface ToastApi {
  show(message: string, tone?: ToastTone): void
}

const noop: ToastApi = { show() {} }
const ToastContext = createContext<ToastApi>(noop)

export function useToast(): ToastApi {
  return useContext(ToastContext)
}

interface ToastItem {
  id: number
  message: string
  tone: ToastTone
}

// 轻提示：右下角（手机在底栏之上），数秒后自动消失；警告用 role=alert，普通用 role=status
export function ToastProvider({ children, durationMs = 4000 }: { children: ReactNode; durationMs?: number }) {
  const [items, setItems] = useState<ToastItem[]>([])
  const seq = useRef(0)
  const timers = useRef(new Set<ReturnType<typeof setTimeout>>())

  const show = useCallback(
    (message: string, tone: ToastTone = 'ok') => {
      const id = ++seq.current
      setItems((list) => [...list.slice(-2), { id, message, tone }])
      const timer = setTimeout(() => {
        timers.current.delete(timer)
        setItems((list) => list.filter((x) => x.id !== id))
      }, durationMs)
      timers.current.add(timer)
    },
    [durationMs],
  )

  useEffect(() => {
    const set = timers.current
    return () => set.forEach(clearTimeout)
  }, [])

  const api = useMemo(() => ({ show }), [show])
  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="pointer-events-none fixed right-4 bottom-4 z-[60] flex w-[min(380px,calc(100vw-32px))] flex-col gap-2 mobile:bottom-20">
        {items.map((it) => (
          <div
            key={it.id}
            role={it.tone === 'warn' ? 'alert' : 'status'}
            className={cn(
              'pointer-events-auto flex items-start gap-2 rounded-[2px] border bg-inv-bg px-3 py-2 text-[13px] leading-[1.5] text-inv-ink',
              it.tone === 'warn' ? 'border-status-warn' : 'border-inv-bg',
            )}
          >
            {it.tone === 'warn' ? (
              <AlertTriangle size={14} className="mt-0.5 shrink-0 text-signal" />
            ) : (
              <Check size={14} className="mt-0.5 shrink-0" />
            )}
            <span className="min-w-0 break-words">{it.message}</span>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}
