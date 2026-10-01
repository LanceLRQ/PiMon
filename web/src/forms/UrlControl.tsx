import { Check, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Input } from '@/ui/input'
import type { ControlProps } from './model'

// url 字段：输入框 + 该字段的校验选项（是否允许查询参数、公网 http、跟随重定向）
export function UrlControl({ field, value, onChange, id, invalid, describedBy }: ControlProps<string>) {
  const { t } = useTranslation()
  const flags: [string, boolean][] = [
    [t('form.urlAllowQuery'), field.allow_query === true],
    [t('form.urlAllowPublicHttp'), field.allow_public_http === true],
    [t('form.urlFollowRedirects'), field.follow_redirects === true],
  ]
  return (
    <div>
      <Input
        id={id}
        value={value}
        invalid={invalid}
        aria-describedby={describedBy}
        inputMode="url"
        spellCheck={false}
        autoCapitalize="off"
        className="font-mono"
        onChange={(e) => onChange(e.target.value)}
      />
      <ul className="mt-1.5 flex flex-wrap gap-x-3.5 gap-y-1 text-[11.5px]" aria-label={t('form.urlRules')}>
        {flags.map(([label, on]) => (
          <li key={label} className={on ? 'inline-flex items-center gap-1 text-ink-2' : 'inline-flex items-center gap-1 text-muted-foreground'}>
            {on ? <Check size={12} aria-hidden /> : <X size={12} aria-hidden />}
            {label}
            <span className="sr-only">{on ? t('form.yes') : t('form.no')}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
