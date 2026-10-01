import { useTranslation } from 'react-i18next'

// 屏幕端应用上线前的占位页，屏幕会话访问 /screen 时显示
export function ScreenPlaceholderPage() {
  const { t } = useTranslation()
  return (
    <main className="grid min-h-screen place-items-center bg-background p-4 text-center text-sm text-muted-foreground">
      <p>{t('shell.screenPlaceholder')}</p>
    </main>
  )
}
