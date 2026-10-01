import { Monitor, Moon, Sun } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { setThemeChoice, useThemeChoice, type ThemeChoice } from '@/admin-theme/theme'
import { setLanguage, type Language } from '@/i18n'
import { Segmented } from '@/ui/segmented'

// 中/EN 切换：只影响当前浏览器，存 localStorage
export function LanguageSwitch({ className }: { className?: string }) {
  const { t, i18n } = useTranslation()
  const current: Language = i18n.language.startsWith('zh') ? 'zh' : 'en'
  return (
    <Segmented<Language>
      ariaLabel={t('shell.language.label')}
      className={className}
      value={current}
      onChange={(lng) => void setLanguage(i18n, lng)}
      options={[
        { value: 'zh', label: t('shell.language.zh'), title: '中文' },
        { value: 'en', label: t('shell.language.en'), title: 'English' },
      ]}
    />
  )
}

// 浅色 / 深色 / 跟随系统
export function ThemeSwitch({ className }: { className?: string }) {
  const { t } = useTranslation()
  const choice = useThemeChoice()
  return (
    <Segmented<ThemeChoice>
      ariaLabel={t('shell.theme.label')}
      className={className}
      value={choice}
      onChange={setThemeChoice}
      options={[
        { value: 'system', label: <Monitor size={16} />, title: t('shell.theme.system') },
        { value: 'light', label: <Sun size={16} />, title: t('shell.theme.light') },
        { value: 'dark', label: <Moon size={16} />, title: t('shell.theme.dark') },
      ]}
    />
  )
}
