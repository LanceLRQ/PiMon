import { Plus, Shield, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/ui/button'
import { Input } from '@/ui/input'
import { FieldError } from '@/ui/section'

interface Props {
  value: string[]
  onChange(next: string[]): void
  // 各行的服务端错误文本（按行下标）
  rowErrors: Record<number, string>
}

// 受信任反代：已添加的行可移除，底部一行输入框添加（回车或点「添加」）
export function TrustedProxies({ value, onChange, rowErrors }: Props) {
  const { t } = useTranslation()
  const [text, setText] = useState('')
  const [dupe, setDupe] = useState(false)

  function add() {
    const v = text.trim()
    if (v === '') return
    if (value.includes(v)) {
      setDupe(true)
      return
    }
    onChange([...value, v])
    setText('')
    setDupe(false)
  }

  return (
    <div className="max-w-[420px]">
      <ul className="rounded-[2px] border border-line-strong bg-card">
        {value.length === 0 && <li className="px-2.5 py-2 text-[12.5px] text-muted-foreground">{t('settings.net.trustedEmpty')}</li>}
        {value.map((v, i) => (
          <li key={v} className="border-t border-border first:border-t-0">
            <div className="flex h-[34px] items-center gap-2 pr-1 pl-2.5 font-mono text-[12.5px]">
              <Shield size={14} className="shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate">{v}</span>
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={t('settings.net.trustedRemove', { value: v })}
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                <X />
              </Button>
            </div>
            {rowErrors[i] && (
              <div className="px-2.5 pb-1.5">
                <FieldError>{rowErrors[i]}</FieldError>
              </div>
            )}
          </li>
        ))}
        <li className="flex h-[38px] items-center gap-2 border-t border-border pr-1 pl-2.5">
          <Plus size={14} className="shrink-0 text-muted-foreground" />
          <Input
            className="h-7 border-0 bg-transparent px-0 font-mono text-[12.5px] shadow-none focus-visible:ring-0"
            placeholder={t('settings.net.trustedPlaceholder')}
            aria-label={t('settings.net.trustedAddAria')}
            value={text}
            onChange={(e) => {
              setText(e.target.value)
              setDupe(false)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                add()
              }
            }}
          />
          <Button size="xs" variant="outline" className="rounded-[2px]" disabled={text.trim() === ''} onClick={add}>
            {t('settings.net.trustedAdd')}
          </Button>
        </li>
      </ul>
      {dupe && <FieldError>{t('settings.net.trustedDuplicate')}</FieldError>}
    </div>
  )
}
