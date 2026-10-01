import { Plus, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/ui/button'
import { Input, PasswordInput } from '@/ui/input'
import { newRowId, type ControlProps, type ErrorMap, type KvEntry, type ListItem } from './model'

interface WithErrors {
  errors: ErrorMap
}

// list：每项一个输入框，可增删；逐项错误按 path[i] 定位
const blankItem: ListItem = { id: 'blank', text: '' }

export function ListControl({ field, value, onChange, id, path, errors }: ControlProps<ListItem[]> & WithErrors) {
  const { t } = useTranslation()
  const items = value.length === 0 ? [blankItem] : value
  const set = (i: number, text: string) => onChange(items.map((x, j) => (j === i ? { ...x, text } : x)))
  // 提交时空项被丢弃，错误路径用的是丢弃后的下标：先换算回表单行
  const compact = items.map((x, i) => (x.text.trim() === '' ? -1 : i)).filter((i) => i >= 0)
  return (
    <div className="flex max-w-[420px] flex-col gap-1.5">
      {items.map((item, i) => {
        const pos = compact.indexOf(i)
        const err = pos >= 0 ? errors[`${path}[${pos}]`] : undefined
        return (
          <div key={item.id} className="flex items-center gap-1.5">
            <Input
              id={i === 0 ? id : undefined}
              value={item.text}
              invalid={!!err}
              aria-label={t('form.listItem', { title: field.title, n: i + 1 })}
              onChange={(e) => set(i, e.target.value)}
              className="font-mono"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={t('form.listRemove', { n: i + 1 })}
              onClick={() => onChange(items.length === 1 ? [] : items.filter((_, j) => j !== i))}
            >
              <X />
            </Button>
          </div>
        )
      })}
      <Button type="button" variant="outline" size="sm" className="w-fit rounded-[2px]" onClick={() => onChange([...value, { id: newRowId(), text: '' }])}>
        <Plus /> {t('form.add')}
      </Button>
    </div>
  )
}

const emptyKv = (id: string = newRowId()): KvEntry => ({ id, key: '', value: '', origKey: '', secret: { text: '', set: false } })
const blankKv = emptyKv('blank')

// kv：键值行；secret_values 时值用密码框，已保存的值显示「已设置」，留空保持不变
export function KvControl({ field, value, onChange, id, path, errors }: ControlProps<KvEntry[]> & WithErrors) {
  const { t } = useTranslation()
  const secret = field.secret_values === true
  const rows = value.length === 0 ? [blankKv] : value
  const patch = (i: number, p: Partial<KvEntry>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...p } : r)))
  return (
    <div className="max-w-[560px]">
      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)_32px] gap-1.5 pb-1 font-mono text-[11px] text-muted-foreground">
        <span>{t('form.kvKey')}</span>
        <span>{secret ? t('form.kvSecretValue') : t('form.kvValue')}</span>
        <span />
      </div>
      <div className="flex flex-col gap-1.5">
        {rows.map((r, i) => {
          const rowErr = errors[`${path}.${r.key.trim()}`]
          return (
            <div key={r.id} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)_32px] items-center gap-1.5">
              <Input
                id={i === 0 ? id : undefined}
                value={r.key}
                aria-label={t('form.kvKeyOf', { n: i + 1 })}
                invalid={rowErr === 'duplicate' || (errors[path] === 'invalid' && r.key.trim() === '')}
                spellCheck={false}
                className="font-mono"
                onChange={(e) => patch(i, { key: e.target.value })}
              />
              {secret ? (
                <PasswordInput
                  value={r.secret.text}
                  aria-label={t('form.kvValueOf', { n: i + 1 })}
                  invalid={rowErr === 'required'}
                  autoComplete="new-password"
                  spellCheck={false}
                  className="font-mono"
                  placeholder={r.secret.set ? t('form.secretKeep') : ''}
                  showLabel={t('form.show')}
                  hideLabel={t('form.hide')}
                  onChange={(e) => patch(i, { secret: { ...r.secret, text: e.target.value } })}
                />
              ) : (
                <Input
                  value={r.value}
                  aria-label={t('form.kvValueOf', { n: i + 1 })}
                  spellCheck={false}
                  className="font-mono"
                  onChange={(e) => patch(i, { value: e.target.value })}
                />
              )}
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={t('form.kvRemove', { n: i + 1 })}
                onClick={() => onChange(rows.length === 1 ? [] : rows.filter((_, j) => j !== i))}
              >
                <X />
              </Button>
            </div>
          )
        })}
      </div>
      <Button type="button" variant="outline" size="sm" className="mt-1.5 rounded-[2px]" onClick={() => onChange([...value, emptyKv()])}>
        <Plus /> {t('form.add')}
      </Button>
    </div>
  )
}
