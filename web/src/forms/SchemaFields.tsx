import { ArrowDown, ArrowUp, Eye, Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/ui/button'
import { cn } from '@/lib/utils'
import { initialValues, visibleKeys } from './build'
import { FieldControl } from './FieldControl'
import { errorAt, joinPath, newRowId, type ControlProps, type ErrorMap, type Field, type FormValues, type ObjectRow } from './model'

interface SchemaFieldsProps {
  fields: Field[]
  values: FormValues
  onChange: (values: FormValues) => void
  errors: ErrorMap
  // 某个路径被用户修改时通知上层，用于清掉该处的服务端错误
  onEdit: (path: string) => void
  prefix?: string
  // row：标签在左（页面主表单）；stack：标签在上（object_list 元素内）
  layout?: 'row' | 'stack'
}

const domId = (path: string) => `f-${path.replace(/[^\w-]+/g, '-')}`

function conditionText(f: Field): string {
  return (f.visible_when ?? []).map((c) => `${c.key} = ${c.values.map(String).join(' | ')}`).join(', ')
}

// 按 config_schema 渲染一组字段：隐藏 visible_when 不成立的字段，错误按路径定位到字段
export function SchemaFields({ fields, values, onChange, errors, onEdit, prefix = '', layout = 'row' }: SchemaFieldsProps) {
  const shown = visibleKeys(fields, values)
  return (
    <>
      {fields
        .filter((f) => shown.has(f.key))
        .map((f) => {
          const path = joinPath(prefix, f.key)
          const set = (v: unknown) => {
            onEdit(path)
            onChange({ ...values, [f.key]: v })
          }
          return (
            <FieldRow key={f.key} field={f} path={path} error={errorAt(errors, path)} layout={layout}>
              {({ id, invalid, describedBy }) =>
                f.type === 'object_list' ? (
                  <ObjectListControl
                    field={f}
                    value={values[f.key] as ObjectRow[]}
                    onChange={set}
                    path={path}
                    id={id}
                    invalid={invalid}
                    describedBy={describedBy}
                    errors={errors}
                    onEdit={onEdit}
                  />
                ) : (
                  <FieldControl
                    field={f}
                    value={values[f.key]}
                    onChange={set}
                    path={path}
                    id={id}
                    invalid={invalid}
                    describedBy={describedBy}
                    errors={errors}
                  />
                )
              }
            </FieldRow>
          )
        })}
    </>
  )
}

interface FieldRowProps {
  field: Field
  path: string
  error: string | undefined
  layout: 'row' | 'stack'
  children: (a: { id: string; invalid: boolean; describedBy: string | undefined }) => React.ReactNode
}

// 一个字段的一行：标签（标题、必填、类型、键、显示条件）+ 控件 + 帮助 + 错误
function FieldRow({ field, path, error, layout, children }: FieldRowProps) {
  const { t } = useTranslation()
  const id = domId(path)
  const helpId = `${id}-help`
  const errId = `${id}-err`
  const cond = conditionText(field)
  const describedBy = [field.help ? helpId : '', error ? errId : ''].filter(Boolean).join(' ') || undefined
  const boolRow = field.type === 'boolean'
  return (
    <div
      data-field={path}
      className={cn(
        'grid gap-x-4 gap-y-1.5 border-t border-border px-4 py-3 first:border-t-0',
        layout === 'row' ? 'grid-cols-[200px_minmax(0,1fr)] mobile:grid-cols-1' : 'grid-cols-1',
        cond && 'bg-signal-soft',
      )}
    >
      <div className="min-w-0 pt-1.5 text-[13px]">
        <label htmlFor={boolRow ? undefined : id} className="font-medium">
          {field.title}
          {field.required && (
            <span className="ml-0.5 text-status-crit" title={t('form.required')} aria-hidden>
              *
            </span>
          )}
        </label>
        {field.required && <span className="sr-only"> {t('form.required')}</span>}
        <div className="mt-0.5 flex flex-wrap items-center gap-1.5 font-mono text-[10.5px] text-muted-foreground">
          <span className="rounded-[2px] border border-border px-1">{field.type}</span>
          <span className="break-all">{field.key}</span>
        </div>
        {cond && (
          <span className="mt-1.5 inline-flex max-w-full items-center gap-1 rounded-[2px] border border-dashed border-signal px-1.5 font-mono text-[10.5px] leading-4 text-signal-text">
            <Eye size={11} aria-hidden />
            <span className="break-all">visible_when: {cond}</span>
          </span>
        )}
      </div>
      <div className="min-w-0">
        {children({ id, invalid: !!error, describedBy })}
        {field.help && (
          <p id={helpId} className="mt-1.5 text-[12px] leading-[1.55] text-muted-foreground">
            {field.help}
          </p>
        )}
        {error && (
          <p id={errId} role="alert" className="mt-1.5 flex items-center gap-1 text-[12px] text-status-crit">
            {t(`form.errors.${error}`, { defaultValue: error })}
          </p>
        )}
      </div>
    </div>
  )
}

interface ObjectListProps extends ControlProps<ObjectRow[]> {
  errors: ErrorMap
  onEdit: (path: string) => void
}

// object_list：一组对象，每个元素按子字段渲染，可增删与上下移动。
// 元素内已保存的密钥靠原下标 ref 找回原值，所以重排不会错配。
function ObjectListControl({ field, value, onChange, path, errors, onEdit }: ObjectListProps) {
  const { t } = useTranslation()
  const subs = field.fields ?? []
  const move = (i: number, d: -1 | 1) => {
    const next = [...value]
    ;[next[i], next[i + d]] = [next[i + d], next[i]]
    onEdit(path)
    onChange(next)
  }
  return (
    <div className="flex flex-col gap-2.5">
      {value.length === 0 && <p className="text-[12.5px] text-muted-foreground">{t('form.objectEmpty')}</p>}
      {value.map((row, i) => (
        <fieldset key={row.id} className="min-w-0 rounded-[2px] border border-line-strong">
          <legend className="sr-only">{t('form.objectRow', { n: i + 1 })}</legend>
          <div className="flex items-center gap-1 border-b border-border bg-panel-2 px-2.5 py-1">
            <span className="font-mono text-[11.5px] text-ink-2">#{i + 1}</span>
            <span className="flex-1" />
            <Button type="button" variant="ghost" size="icon-xs" aria-label={t('form.objectUp', { n: i + 1 })} disabled={i === 0} onClick={() => move(i, -1)}>
              <ArrowUp />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={t('form.objectDown', { n: i + 1 })}
              disabled={i === value.length - 1}
              onClick={() => move(i, 1)}
            >
              <ArrowDown />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={t('form.objectRemove', { n: i + 1 })}
              onClick={() => {
                onEdit(path)
                onChange(value.filter((_, j) => j !== i))
              }}
            >
              <Trash2 />
            </Button>
          </div>
          <SchemaFields
            fields={subs}
            values={row.values}
            onChange={(vals) => onChange(value.map((r, j) => (j === i ? { ...r, values: vals } : r)))}
            errors={errors}
            onEdit={onEdit}
            prefix={`${path}[${i}]`}
            layout="stack"
          />
        </fieldset>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="w-fit rounded-[2px]"
        onClick={() => {
          onEdit(path)
          onChange([...value, { id: newRowId(), values: initialValues(subs, {}) }])
        }}
      >
        <Plus /> {t('form.addRow')}
      </Button>
    </div>
  )
}
