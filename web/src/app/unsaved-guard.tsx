import { useContext, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { UNSAFE_DataRouterContext, useBlocker } from 'react-router'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'

interface UnsavedGuardProps {
  // 有未保存的修改时为 true
  dirty: boolean
  // 对话框文案的 i18n 键前缀，需要 title、body、stay、leave 四项
  textKey: string
}

// 离开页面前的未保存修改保护：
// - 路由切换（侧栏、链接、前进后退）用自建对话框确认，不使用 window.confirm；
// - 关闭或刷新标签页只能走浏览器自带的确认（浏览器不允许自定义文案）。
export function UnsavedGuard({ dirty, textKey }: UnsavedGuardProps) {
  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      // 部分浏览器要求设置 returnValue 才会弹出确认
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])

  // useBlocker 只能用在数据路由里；其他路由环境（个别测试）退化为只保护关闭标签页
  const dataRouter = useContext(UNSAFE_DataRouterContext)
  return dataRouter ? <RouteBlocker dirty={dirty} textKey={textKey} /> : null
}

function RouteBlocker({ dirty, textKey }: UnsavedGuardProps) {
  const { t } = useTranslation()
  const blocker = useBlocker(dirty)
  const blocked = blocker.state === 'blocked'
  return (
    <Dialog open={blocked} onOpenChange={(o) => !o && blocker.state === 'blocked' && blocker.reset()}>
      {blocked && (
        <DialogContent
          tag="!"
          tagTone="crit"
          title={t(`${textKey}.title`)}
          footer={
            <>
              <Button variant="outline" className="rounded-[2px]" onClick={() => blocker.reset()}>
                {t(`${textKey}.stay`)}
              </Button>
              <Button variant="destructive" className="rounded-[2px]" onClick={() => blocker.proceed()}>
                {t(`${textKey}.leave`)}
              </Button>
            </>
          }
        >
          <p>{t(`${textKey}.body`)}</p>
        </DialogContent>
      )}
    </Dialog>
  )
}
