import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { I18nextProvider } from 'react-i18next'
import { initTheme } from '@/admin-theme/theme'
import { initI18n } from '@/i18n'
import { App } from '@/app/App'
import './index.css'

initTheme()
initI18n().then((i18n) => {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <I18nextProvider i18n={i18n}>
        <App />
      </I18nextProvider>
    </StrictMode>,
  )
})
