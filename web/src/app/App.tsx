import { useTranslation } from 'react-i18next'

// 占位首页：外壳、路由与会话守卫在 C4 重做
export function App() {
  const { t } = useTranslation()
  return (
    <main className="mx-auto flex min-h-screen max-w-xl flex-col justify-center gap-2 px-4">
      <h1 className="font-mono text-2xl font-medium">{t('app.name')}</h1>
      <p className="text-muted-foreground">{t('app.tagline')}</p>
      <p className="text-sm text-muted-foreground">{t('app.placeholder')}</p>
    </main>
  )
}
