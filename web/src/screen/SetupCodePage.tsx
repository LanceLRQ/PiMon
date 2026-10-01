import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@/api/errors'
import { http } from '@/api/client'
import { fetchSession } from '@/api/session'
import { useSession } from '@/app/session'
import type { SetupCodeReveal } from '@/types/generated'
import { ShellFrame } from './ScreenNotices'

function isLoopback(hostname: string): boolean {
  return hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '::1' || hostname === '[::1]'
}

/**
 * 手机访问地址：取当前页面的协议、主机与端口。kiosk 用回环地址打开本页时手机用不了，主机处提示换成树莓派地址。
 * 无端口说明走默认端口（如 nginx 反代），不补端口。
 */
export function phoneAddress(loc: Pick<Location, 'protocol' | 'hostname' | 'port'>, placeholder: string): string {
  const host = isLoopback(loc.hostname) ? `<${placeholder}>` : loc.hostname
  return `${loc.protocol}//${host}${loc.port ? `:${loc.port}` : ''}`
}

export interface SetupCodePageProps {
  /** 轮询间隔：取设置码并重新查询会话 */
  pollMs: number
}

/**
 * 还没有管理员时屏幕端显示的设置码页：大字显示设置码与手机访问地址。
 * 定期重新取设置码（过期后会换新）并重新查询会话，管理员设置完成后会话的 needs_setup 变为 false，守卫自动切到屏幕。
 */
export function SetupCodePage({ pollMs }: SetupCodePageProps) {
  const { t } = useTranslation()
  const { refresh } = useSession()
  const [code, setCode] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      try {
        // 先看会话：管理员一旦设置完成，设置码接口会回 409，浏览器会把它记成控制台错误，所以不再去取；
        // 设置完成（或会话失效）时交给 refresh 更新全局会话，守卫随即切换页面
        const current = await fetchSession()
        if (!current.needs_setup || !current.authenticated) {
          if (!cancelled) await refresh()
          return
        }
        const r = await http.get<SetupCodeReveal>('/api/screen/setup-code')
        if (!cancelled) setCode(r.code)
      } catch (e) {
        // 404：暂时没有有效设置码；网络错误保持现状，下一轮再试
        if (!cancelled && e instanceof ApiError && e.status === 404) setCode(null)
      }
      if (!cancelled) timer = setTimeout(() => void tick(), pollMs)
    }
    void tick()
    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
    }
  }, [refresh, pollMs])

  const url = phoneAddress(window.location, t('screenApp.setup.raspberryHost'))
  return (
    <ShellFrame>
      <h1 className="text-3xl font-semibold">{t('screenApp.setup.title')}</h1>
      <p className="text-lg text-s-muted-fg">{t('screenApp.setup.codeLabel')}</p>
      {code ? (
        <p data-setup-code className="font-mono text-5xl font-bold tracking-wider">
          {code}
        </p>
      ) : (
        <p className="text-2xl text-s-muted-fg">{t('screenApp.setup.unavailable')}</p>
      )}
      <p className="text-xl">{t('screenApp.setup.openOnPhone', { url })}</p>
      <p className="max-w-xl text-base text-s-muted-fg">{t('screenApp.setup.hint')}</p>
    </ShellFrame>
  )
}
