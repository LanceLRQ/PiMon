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

function ChunkFailed() {
  const { t } = useTranslation()
  return (
    <div role="alert" className="flex flex-col items-start gap-3 p-6 text-sm mobile:p-3.5">
      <p>{t('shell.pageLoadFailed')}</p>
      <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
        {t('shell.reload')}
      </Button>
    </div>
  )
}

// 懒加载的页面代码块拉取失败（常见于中枢升级后旧文件名失效）时给出可操作的提示，而不是白屏
export class LazyBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('页面渲染失败', error, info.componentStack)
  }

  render() {
    return this.state.failed ? <ChunkFailed /> : this.props.children
  }
}
