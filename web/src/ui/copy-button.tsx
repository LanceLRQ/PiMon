import { Check, Copy } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/ui/button'
import { useToast } from '@/ui/toast'

// 复制文本：优先 navigator.clipboard；http 等非安全上下文里它不存在，退回隐藏 textarea + execCommand。
export async function copyText(text: string): Promise<boolean> {
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard && window.isSecureContext !== false) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 权限被拒时继续走回退
  }
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(ta)
  }
}

interface CopyButtonProps {
  text: string
  // 无障碍名称与提示，缺省为「复制」
  label?: string
  className?: string
}

export function CopyButton({ text, label, className }: CopyButtonProps) {
  const { t } = useTranslation()
  const toast = useToast()
  const [done, setDone] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const name = label ?? t('common.copy')

  async function onClick() {
    const ok = await copyText(text)
    if (!ok) {
      toast.show(t('common.copyFailed'), 'warn')
      return
    }
    setDone(true)
    toast.show(t('common.copied'))
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setDone(false), 1500)
  }

  return (
    <Button type="button" variant="outline" size="icon-xs" className={className} aria-label={name} title={name} onClick={() => void onClick()}>
      {done ? <Check /> : <Copy />}
    </Button>
  )
}
