import { Component, type ErrorInfo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/ui/button'

// 页面代码块加载期间的轻量占位：沿用外壳的加载提示样式，不闪烁、不占整屏
export function RouteFallback() {
  const { t } = useTranslation()
  return (
    <div role="status" className="p-6 text-sm text-muted-foreground mobile:p-3.5">
      {t('shell.loading')}
    </div>
  )
}

// 动态 import 拉取失败的典型报错文案（Chromium、Firefox、Safari 与打包器各不相同）
const chunkErrorPattern = /dynamically imported module|importing a module script failed|ChunkLoadError|loading chunk|error loading dynamically/i

export function isChunkLoadError(error: unknown): boolean {
  return error instanceof Error && (chunkErrorPattern.test(error.message) || error.name === 'ChunkLoadError')
}

function FailedNotice({ messageKey }: { messageKey: 'shell.pageLoadFailed' | 'shell.pageError' }) {
  const { t } = useTranslation()
  return (
    <div role="alert" className="flex flex-col items-start gap-3 p-6 text-sm mobile:p-3.5">
      <p>{t(messageKey)}</p>
      <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
        {t('shell.reload')}
      </Button>
    </div>
  )
}

// 页面出错时给出可操作的提示而不是白屏：代码块拉取失败（常见于中枢升级后旧文件名失效）
// 提示刷新以获取新版本；其他渲染异常给通用文案，并保留控制台详情
export class LazyBoundary extends Component<{ children: ReactNode }, { error: unknown }> {
  state: { error: unknown } = { error: null }

  static getDerivedStateFromError(error: unknown) {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('页面渲染失败', error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (error === null) return this.props.children
    return <FailedNotice messageKey={isChunkLoadError(error) ? 'shell.pageLoadFailed' : 'shell.pageError'} />
  }
}
