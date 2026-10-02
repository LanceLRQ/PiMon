import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { I18nextProvider } from 'react-i18next'
import { initTheme } from '@/admin-theme/theme'
import { initI18n } from '@/i18n'
import { App } from '@/app/App'
import { isScreenPath } from '@/screen/path'
import './index.css'

// 屏幕端有自己的主题体系（data-theme），管理端的 dark 类不得出现在 /screen 下
if (!isScreenPath(location.pathname)) initTheme()
initI18n().then((i18n) => {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <I18nextProvider i18n={i18n}>
        <App />
      </I18nextProvider>
    </StrictMode>,
  )
})
