import type { ReactNode } from 'react'
import { LanguageSwitch, ThemeSwitch } from '@/app/preferences'
import { PiMonLogo } from '@/ui/pimon-logo'

interface AuthShellProps {
  // 顶栏左侧的小字（如「pimon-hub」）
  topLabel: string
  title: string
  // 品牌区第二行，通常是访问地址
  subtitle: string
  // 内容宽度类，登录页窄、首次设置页宽
  widthClass: string
  children: ReactNode
}

// 登录与首次设置共用的独立页外壳：顶栏（语言、主题）+ 品牌区 + 居中内容
export function AuthShell({ topLabel, title, subtitle, widthClass, children }: AuthShellProps) {
  return (
    <div className="min-h-screen overflow-auto bg-background text-foreground">
      <div className="fixed inset-x-0 top-0 flex h-12 items-center gap-2.5 px-5 font-mono text-[11.5px] text-muted-foreground">
        <span>{topLabel}</span>
        <span className="flex-1" />
        <LanguageSwitch />
        <ThemeSwitch />
      </div>
      <main className="flex min-h-screen flex-col items-center justify-center px-5 py-8 mobile:justify-start mobile:pt-[72px]">
        <div className={`w-full ${widthClass}`}>
          <div className="mb-5 flex items-center gap-3">
            <PiMonLogo className="size-11" />
            <div className="min-w-0">
              <h1 className="block text-[22px] leading-[1.1] font-medium">{title}</h1>
              <small className="block truncate font-mono text-[11.5px] text-muted-foreground">{subtitle}</small>
            </div>
          </div>
          {children}
        </div>
      </main>
    </div>
  )
}

// 访问地址：用于品牌区第二行
export function hostLabel(): string {
  return window.location.host
}

// 当前页面是否经 HTTP 明文访问
export function isPlainHttp(protocol: string = window.location.protocol): boolean {
  return protocol === 'http:'
}
