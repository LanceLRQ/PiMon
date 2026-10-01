import { useTranslation } from 'react-i18next'
import { Input } from '@/ui/input'
import { Segmented } from '@/ui/segmented'
import { Select } from '@/ui/select'
import { Switch } from '@/ui/switch'
import { parseDurationSeconds } from './duration'
import type { ControlProps } from './model'

// 单行文本
export function StringControl({ value, onChange, id, invalid, describedBy }: ControlProps<string>) {
  return <Input id={id} value={value} invalid={invalid} aria-describedby={describedBy} onChange={(e) => onChange(e.target.value)} />
}

// 多行文本
export function TextControl({ value, onChange, id, invalid, describedBy }: ControlProps<string>) {
  return (
    <textarea
      id={id}
      value={value}
      rows={4}
      aria-invalid={invalid || undefined}
      aria-describedby={describedBy}
      onChange={(e) => onChange(e.target.value)}
      className="w-full min-w-0 rounded-[2px] border border-line-strong bg-card px-2.5 py-1.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring aria-invalid:border-status-crit"
    />
  )
}

// 数字：输入框 + 范围与默认值提示
export function NumberControl({ field, value, onChange, id, invalid, describedBy }: ControlProps<string>) {
  const { t } = useTranslation()
  const hasRange = field.min !== undefined || field.max !== undefined
  const range = hasRange ? `${field.min ?? ''}–${field.max ?? ''}` : ''
  const def = typeof field.default === 'number' ? t('form.numberDefault', { value: field.default }) : ''
  return (
    <div className="flex flex-wrap items-center gap-2.5">
      <Input
        id={id}
        type="number"
        inputMode="decimal"
        value={value}
        invalid={invalid}
        aria-describedby={describedBy}
        onChange={(e) => onChange(e.target.value)}
        className="w-[190px] font-mono mobile:w-full"
        min={field.min}
        max={field.max}
      />
      {(range || def) && (
        <span className="font-mono text-[11.5px] text-muted-foreground">{[range, def].filter(Boolean).join(' · ')}</span>
      )}
    </div>
  )
}

export function BooleanControl({ field, value, onChange, id }: ControlProps<boolean>) {
  const { t } = useTranslation()
  return (
    <div className="flex items-center gap-2.5 pt-1">
      <Switch id={id} checked={value} onChange={onChange} ariaLabel={field.title} />
      <span className="text-[12.5px] text-ink-2">{value ? t('form.on') : t('form.off')}</span>
    </div>
  )
}

// 枚举：选项不多时用分段选择，否则下拉
export function EnumControl({ field, value, onChange, id, invalid, describedBy }: ControlProps<string>) {
  const { t } = useTranslation()
  const options = field.options ?? []
  // 选项少且名称短时用分段选择；名称较长（会折行）改用下拉
  if (options.length > 0 && options.length <= 4 && options.every((o) => [...o.title].length <= 10)) {
    return (
      <Segmented
        ariaLabel={field.title}
        value={value}
        onChange={onChange}
        className="max-w-full flex-wrap"
        options={options.map((o) => ({ value: o.value, label: o.title, title: o.title }))}
      />
    )
  }
  return (
    <Select id={id} value={value} invalid={invalid} aria-describedby={describedBy} onChange={(e) => onChange(e.target.value)} className="max-w-[320px]">
      {!field.required && <option value="">{t('form.enumNone')}</option>}
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.title}
        </option>
      ))}
    </Select>
  )
}

// 常用时长预设，按字段 min/max（秒）过滤
const durationPresets = ['5s', '10s', '30s', '1m', '5m', '15m', '1h']

export function DurationControl({ field, value, onChange, id, invalid, describedBy }: ControlProps<string>) {
  const { t } = useTranslation()
  const presets = durationPresets.filter((p) => {
    const s = parseDurationSeconds(p) ?? 0
    return (field.min === undefined || s >= field.min) && (field.max === undefined || s <= field.max)
  })
  return (
    <div className="flex flex-wrap items-center gap-2.5">
      <Input
        id={id}
        value={value}
        invalid={invalid}
        aria-describedby={describedBy}
        onChange={(e) => onChange(e.target.value)}
        className="w-[120px] font-mono mobile:w-full"
        spellCheck={false}
      />
      {presets.length > 0 && (
        <Segmented
          ariaLabel={t('form.durationPresets')}
          value={presets.includes(value.trim()) ? value.trim() : ''}
          onChange={onChange}
          options={presets.map((p) => ({ value: p, label: <span className="font-mono">{p}</span>, title: p }))}
          className="max-w-full flex-wrap"
        />
      )}
      <span className="text-[11.5px] text-muted-foreground">{t('form.durationFormat')}</span>
    </div>
  )
}
