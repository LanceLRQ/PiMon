import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/ui/page-header'

export function NotFoundPage() {
  const { t } = useTranslation()
  return <PageHeader title={t('shell.notFound')} />
}
