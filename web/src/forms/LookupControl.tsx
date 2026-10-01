import { MapPin, X } from 'lucide-react'
import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import { cn } from '@/lib/utils'
import { Button } from '@/ui/button'
import { useFormContext, type LookupCandidate } from './context'
import type { ControlProps, LookupValue } from './model'

// 输入停顿多久后才发起候选查询
export const lookupDebounceMs = 300

// lookup 字段：输入关键字，停顿后由插件给出候选，选中后保存候选的原始值
export function LookupControl({ field, value, onChange, id, invalid, describedBy }: ControlProps<LookupValue | null>) {
  const { t, i18n } = useTranslation()
  const { lookup } = useFormContext()
  const listId = useId()
  const [query, setQuery] = useState('')
  const [editing, setEditing] = useState(false)
  const [open, setOpen] = useState(false)
  const [state, setState] = useState<'idle' | 'loading' | 'done' | 'error'>('idle')
  const [candidates, setCandidates] = useState<LookupCandidate[]>([])
  const [error, setError] = useState('')
  const [active, setActive] = useState(0)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const abort = useRef<AbortController | null>(null)
  const root = useRef<HTMLDivElement>(null)

  const cancel = useCallback(() => {
    clearTimeout(timer.current)
    abort.current?.abort()
    abort.current = null
  }, [])
  useEffect(() => cancel, [cancel])

  // 点击控件外部收起候选
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const search = (q: string) => {
    cancel()
    if (q.trim() === '') {
      setState('idle')
      setCandidates([])
      return
    }
    setState('loading')
    timer.current = setTimeout(() => {
      const ctrl = new AbortController()
      abort.current = ctrl
      lookup(field.key, q.trim(), ctrl.signal).then(
        (list) => {
          if (ctrl.signal.aborted) return
          setCandidates(list)
          setActive(0)
          setState('done')
        },
        (e: unknown) => {
          if (ctrl.signal.aborted) return
          setError(isApiError(e) && e.code === 'validation.failed' ? t('form.lookupUnsupported') : translateErrorValue(i18n, e))
          setState('error')
        },
      )
    }, lookupDebounceMs)
  }

  const pick = (c: LookupCandidate) => {
    cancel()
    onChange({ value: c.value, label: c.label })
    setEditing(false)
    setOpen(false)
    setQuery('')
    setState('idle')
  }

  const text = editing || !value ? query : value.label
  const showList = open && editing && state !== 'idle'

  return (
    <div ref={root} className="relative max-w-[420px]">
      <div className="relative">
        <MapPin size={13} aria-hidden className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted-foreground" />
        <input
          id={id}
          role="combobox"
          aria-expanded={showList}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-invalid={invalid || undefined}
          aria-describedby={describedBy}
          aria-activedescendant={showList && candidates[active] ? `${listId}-${active}` : undefined}
          autoComplete="off"
          value={text}
          placeholder={t('form.lookupPlaceholder')}
          onFocus={() => {
            if (editing) setOpen(true)
          }}
          onChange={(e) => {
            // 重新输入即视为放弃已选值，必须重新选择候选
            if (value) onChange(null)
            setEditing(true)
            setOpen(true)
            setQuery(e.target.value)
            search(e.target.value)
          }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown' && candidates.length > 0) {
              e.preventDefault()
              setActive((a) => (a + 1) % candidates.length)
            } else if (e.key === 'ArrowUp' && candidates.length > 0) {
              e.preventDefault()
              setActive((a) => (a - 1 + candidates.length) % candidates.length)
            } else if (e.key === 'Enter' && showList && candidates[active]) {
              e.preventDefault()
              pick(candidates[active])
            } else if (e.key === 'Escape') {
              setOpen(false)
            }
          }}
          className={cn(
            'h-8 w-full min-w-0 rounded-[2px] border border-line-strong bg-card pr-8 pl-8 text-[13px] outline-none',
            'placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring',
            invalid && 'border-status-crit shadow-[inset_0_0_0_1px_var(--status-crit)]',
          )}
        />
        {(value || query) && (
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t('form.lookupClear')}
            className="absolute top-1/2 right-1.5 -translate-y-1/2"
            onClick={() => {
              cancel()
              onChange(null)
              setQuery('')
              setEditing(false)
              setState('idle')
            }}
          >
            <X />
          </Button>
        )}
      </div>
      <ul
        id={listId}
        role="listbox"
        hidden={!showList}
        aria-label={t('form.lookupCandidates')}
        className="absolute top-full right-0 left-0 z-20 mt-0.5 max-h-60 overflow-auto rounded-[2px] border border-line-strong bg-card shadow-[0_8px_20px_rgb(0_0_0/0.15)]"
      >
        {state === 'loading' && <li className="px-3 py-2 text-[12px] text-muted-foreground">{t('form.lookupSearching')}</li>}
        {state === 'error' && (
          <li role="alert" className="px-3 py-2 text-[12px] text-status-crit">
            {error}
          </li>
        )}
        {state === 'done' && candidates.length === 0 && <li className="px-3 py-2 text-[12px] text-muted-foreground">{t('form.lookupNoMatch')}</li>}
        {state === 'done' &&
          candidates.map((c, i) => (
            <li
              key={c.value}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => pick(c)}
              className={cn('flex cursor-pointer items-center gap-2 px-3 py-1.5 text-[13px]', i === active && 'bg-panel-2')}
            >
              <MapPin size={13} aria-hidden className="shrink-0 text-muted-foreground" />
              <span className="min-w-0 truncate">{c.label}</span>
            </li>
          ))}
      </ul>
    </div>
  )
}
