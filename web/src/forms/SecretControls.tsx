import { Check } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { PasswordInput } from '@/ui/input'
import type { ControlProps, SecretValue } from './model'

// 密钥输入：只写不读。已保存过时显示「已设置」，留空表示保持不变，输入新值则覆盖。
export function SecretControl({ field, value, onChange, id, invalid, describedBy }: ControlProps<SecretValue>) {
  const { t } = useTranslation()
  return (
    <div className="max-w-[420px]">
      <PasswordInput
        id={id}
        value={value.text}
        invalid={invalid}
        aria-describedby={describedBy}
        autoComplete="new-password"
        spellCheck={false}
        className="font-mono"
        placeholder={value.set ? t('form.secretKeep') : field.type === 'secret_url' ? t('form.secretUrlNew') : t('form.secretNew')}
        showLabel={t('form.show')}
        hideLabel={t('form.hide')}
        onChange={(e) => onChange({ ...value, text: e.target.value })}
      />
      {value.set && value.text === '' && (
        <span className="mt-1 inline-flex items-center gap-1 text-[11.5px] text-status-ok">
          <Check size={12} aria-hidden /> {t('form.secretIsSet')}
        </span>
      )}
    </div>
  )
}
