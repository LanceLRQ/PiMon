import { useTranslation } from 'react-i18next'
import { PageHeader } from './page-header'

interface PlaceholderPageProps {
  no?: string
  titleKey: string
  // 页面所属后续里程碑；不传表示由本里程碑后续任务实现
  milestone?: 'M1d'
}

// 路由占位页：后续页面任务用真实页面替换，标题仍走 i18n
export function PlaceholderPage({ no, titleKey, milestone }: PlaceholderPageProps) {
  const { t } = useTranslation()
  return (
    <>
      <PageHeader no={no} title={t(titleKey)} />
      <div className="p-6 mobile:p-3">
        <p className="text-sm text-muted-foreground">{milestone ? t('placeholder.m1d') : t('placeholder.pending')}</p>
      </div>
    </>
  )
}
