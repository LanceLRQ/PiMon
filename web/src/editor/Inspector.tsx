import { Ban, Plus, Trash2, X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { WidgetIcon, widgetIconNames } from '@/templates'
import type { Grid, Instance, LayoutScreen, LayoutWidget, PluginInfo, WidgetRef, WidgetSize } from '@/types/generated'
import { cn } from '@/lib/utils'
import { NumberTag } from '@/ui/numbered-label'
import { Segmented } from '@/ui/segmented'
import { Select } from '@/ui/select'
import { Stepper } from '@/ui/stepper'
import { Switch } from '@/ui/switch'
import { occupiedCells, sizeToKey, parseSizeKey, type Cell } from './grid-ops'
import { buildThreshold, readThresholdForm, type OptionsPatch } from './options'
import { toRect } from './state'

export interface InspectorProps {
  widget: LayoutWidget | null
  screen: LayoutScreen | undefined
  grid: Grid
  plugins: readonly PluginInfo[]
  instances: readonly Instance[]
  /** 选中小组件当前的模板（plugin 来源随尺寸变化） */
  template: string
  allowedSizes: readonly WidgetSize[]
  onMove: (cell: Cell) => void
  onResize: (size: WidgetSize) => void
  onOptions: (patch: OptionsPatch) => void
  onBinding: (binding: LayoutWidget['binding']) => void
  onRemove: () => void
}

function Field({ no, label, hint, highlight, children }: { no: string; label: ReactNode; hint?: ReactNode; highlight?: boolean; children: ReactNode }) {
  return (
    <div className="border-b border-border px-3.5 py-3">
      <div className="mb-2 flex items-center gap-2 text-[12.5px] font-medium">
        <NumberTag no={no} highlight={highlight} />
        {label}
        {hint && <span className="text-[11.5px] font-normal text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </div>
  )
}

const inputCls =
  'h-8 w-full rounded-[2px] border border-line-strong bg-card px-2.5 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring'

/** 数值输入：本地保留输入过程中的文本（如「-」「1.」），能解析时才提交，清空视为未设置 */
function NumberField({ value, onCommit, label }: { value: number | undefined; onCommit: (v: number | undefined) => void; label: string }) {
  const [text, setText] = useState(value === undefined ? '' : String(value))
  const [seen, setSeen] = useState(value)
  // 外部改了值（如撤销）而输入框里的文本与之不符时同步；自己输入引起的变化不改动文本
  if (value !== seen) {
    setSeen(value)
    const typed = text.trim() === '' ? undefined : Number(text)
    if (typed !== value) setText(value === undefined ? '' : String(value))
  }
  return (
    <input
      type="text"
      inputMode="decimal"
      aria-label={label}
      value={text}
      onChange={(e) => {
        const raw = e.target.value
        setText(raw)
        if (raw.trim() === '') return onCommit(undefined)
        const n = Number(raw)
        if (Number.isFinite(n)) onCommit(n)
      }}
      className={cn(inputCls, 'font-mono')}
    />
  )
}

/** 一个数据项引用的选择：实例 + 该实例所属插件声明的数据项 */
function RefPicker({ instances, plugins, value, onChange, idPrefix }: { instances: readonly Instance[]; plugins: readonly PluginInfo[]; value: WidgetRef | null; onChange: (r: WidgetRef) => void; idPrefix: string }) {
  const { t } = useTranslation()
  const [draftInstance, setDraftInstance] = useState('')
  const instanceId = value?.instance_id ?? draftInstance
  const inst = instances.find((i) => i.id === instanceId)
  const outputs = plugins.find((p) => p.id === inst?.plugin_id)?.outputs ?? []
  return (
    <div className="flex flex-col gap-1.5">
      <Select
        aria-label={t('layoutEd.insp.instance')}
        data-testid={`${idPrefix}-instance`}
        value={instanceId}
        onChange={(e) => {
          setDraftInstance(e.target.value)
          const first = plugins.find((p) => p.id === instances.find((i) => i.id === e.target.value)?.plugin_id)?.outputs[0]
          if (first) onChange({ instance_id: e.target.value, item: first.key })
        }}
      >
        <option value="" disabled>
          {t('layoutEd.insp.pickInstance')}
        </option>
        {instances.map((i) => (
          <option key={i.id} value={i.id}>
            {i.name}
          </option>
        ))}
      </Select>
      <Select
        aria-label={t('layoutEd.insp.item')}
        data-testid={`${idPrefix}-item`}
        value={value?.item ?? ''}
        disabled={!inst}
        onChange={(e) => onChange({ instance_id: instanceId, item: e.target.value })}
      >
        <option value="" disabled>
          {t('layoutEd.insp.pickItem')}
        </option>
        {outputs.map((o) => (
          <option key={o.key} value={o.key}>
            {o.title} · {o.key}
          </option>
        ))}
      </Select>
    </div>
  )
}

function BindingField({ widget, instances, plugins, onBinding }: Pick<InspectorProps, 'instances' | 'plugins' | 'onBinding'> & { widget: LayoutWidget }) {
  const { t } = useTranslation()
  const [addDraft, setAddDraft] = useState<WidgetRef | null>(null)
  if (widget.source === 'plugin') {
    const own = instances.filter((i) => i.plugin_id === widget.plugin_id)
    return (
      <Select
        aria-label={t('layoutEd.insp.instance')}
        data-testid="insp-instance"
        value={widget.binding.instance_id ?? ''}
        onChange={(e) => onBinding(e.target.value ? { instance_id: e.target.value } : {})}
      >
        <option value="">{t('layoutEd.insp.autoInstance')}</option>
        {own.map((i) => (
          <option key={i.id} value={i.id}>
            {i.name}
          </option>
        ))}
        {widget.binding.instance_id && !own.some((i) => i.id === widget.binding.instance_id) && (
          <option value={widget.binding.instance_id}>{t('layoutEd.insp.missingInstance')}</option>
        )}
      </Select>
    )
  }
  const refs = widget.binding.refs ?? []
  if (widget.source === 'generic') {
    return <RefPicker idPrefix="insp-ref" instances={instances} plugins={plugins} value={refs[0] ?? null} onChange={(r) => onBinding({ refs: [r] })} />
  }
  return (
    <div className="flex flex-col gap-2">
      <ul className="flex flex-col gap-1">
        {refs.map((r, i) => (
          <li key={`${r.instance_id}:${r.item}:${i}`} className="flex items-center gap-1.5 rounded-[2px] border border-border px-2 py-1 text-[12px]">
            <span className="min-w-0 flex-1 truncate">
              {instances.find((x) => x.id === r.instance_id)?.name ?? t('layoutEd.insp.missingInstance')} · <span className="font-mono">{r.item}</span>
            </span>
            <button
              type="button"
              aria-label={t('layoutEd.insp.removeRef', { n: i + 1 })}
              onClick={() => onBinding({ refs: refs.filter((_, j) => j !== i) })}
              className="grid size-5 place-items-center text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            >
              <X size={12} />
            </button>
          </li>
        ))}
      </ul>
      <RefPicker idPrefix="insp-add" instances={instances} plugins={plugins} value={addDraft} onChange={setAddDraft} />
      <button
        type="button"
        disabled={!addDraft}
        onClick={() => {
          if (!addDraft) return
          onBinding({ refs: [...refs, addDraft] })
          setAddDraft(null)
        }}
        className="inline-flex h-8 items-center justify-center gap-1.5 rounded-[2px] border border-line-strong text-[13px] outline-none hover:bg-panel-2 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-40"
      >
        <Plus size={14} />
        {t('layoutEd.insp.addRef')}
      </button>
    </div>
  )
}

const thresholdTemplates = new Set(['value', 'gauge'])

/** 右栏：检查器。没选中小组件时显示当前 screen 的概要 */
export function Inspector(props: InspectorProps) {
  const { widget, screen, grid, plugins, instances, template, allowedSizes } = props
  const { t } = useTranslation()

  if (!widget) {
    const rects = (screen?.widgets ?? []).map(toRect)
    return (
      <div data-testid="inspector">
        <div className="border-b border-border px-3.5 py-3">
          <b className="block text-[14px] font-medium">
            <span className="font-mono">{screen?.id ?? '—'}</span> · {screen?.name}
          </b>
          <small className="text-[12px] text-muted-foreground">{t('layoutEd.insp.screenHint')}</small>
        </div>
        <Field no="p1" label={t('layoutEd.insp.summary')}>
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[12.5px]">
            <dt className="text-muted-foreground">{t('layoutEd.insp.grid')}</dt>
            <dd className="text-right font-mono">{grid.cols}×{grid.rows}</dd>
            <dt className="text-muted-foreground">{t('layoutEd.insp.widgets')}</dt>
            <dd className="text-right font-mono">{rects.length}</dd>
            <dt className="text-muted-foreground">{t('layoutEd.insp.occupied')}</dt>
            <dd className="text-right font-mono">{occupiedCells(grid, rects)} / {grid.cols * grid.rows}</dd>
          </dl>
        </Field>
      </div>
    )
  }

  const plugin = plugins.find((p) => p.id === widget.plugin_id)
  const decl = plugin?.widgets?.find((w) => w.id === widget.widget_id)
  const options = widget.options ?? {}
  const title = typeof options.title === 'string' ? options.title : ''
  const icon = typeof options.icon === 'string' ? options.icon : ''
  const thr = readThresholdForm(options.threshold)
  const sizeOptions = allowedSizes.map((s) => ({ value: sizeToKey(s), label: `${s.cols}×${s.rows}`, title: `${s.cols}×${s.rows}` }))
  const setThreshold = (patch: Partial<typeof thr>) => props.onOptions({ threshold: buildThreshold({ ...thr, ...patch }) })

  return (
    <div data-testid="inspector" data-widget-id={widget.id}>
      <div className="border-b border-border px-3.5 py-3">
        <b className="block text-[14px] font-medium">{decl?.name ?? (widget.template ? t(`layoutEd.template.${widget.template}`, { defaultValue: widget.template }) : widget.id)}</b>
        <small className="font-mono text-[11.5px] text-muted-foreground">{widget.plugin_id ?? widget.template} · {template || '—'}</small>
      </div>
      <Field no="p1" label={t('layoutEd.insp.source')}>
        <div className="flex items-center justify-between text-[12.5px]">
          <span>{t(`layoutEd.lib.tab.${widget.source}`)}</span>
          <span className="font-mono text-muted-foreground">{widget.plugin_id ? `${widget.plugin_id}/${widget.widget_id}` : widget.template}</span>
        </div>
      </Field>
      <Field no="p2" label={t('layoutEd.insp.binding')}>
        <BindingField widget={widget} instances={instances} plugins={plugins} onBinding={props.onBinding} />
      </Field>
      <Field no="p3" label={t('layoutEd.insp.size')} hint={t(`layoutEd.insp.sizeFrom.${widget.source === 'plugin' ? 'plugin' : 'catalog'}`)}>
        <Segmented
          ariaLabel={t('layoutEd.insp.size')}
          className="w-full"
          options={sizeOptions}
          value={sizeToKey(widget.size)}
          onChange={(v) => {
            const s = parseSizeKey(v)
            if (s) props.onResize(s)
          }}
        />
      </Field>
      <Field no="p4" label={t('layoutEd.insp.position')} hint={t('layoutEd.insp.positionHint')} highlight>
        <div className="flex flex-wrap gap-2">
          <Stepper
            value={widget.col + 1}
            min={1}
            max={Math.max(1, grid.cols - widget.size.cols + 1)}
            unit={t('layoutEd.insp.col')}
            ariaLabel={t('layoutEd.insp.col')}
            decrementLabel={t('layoutEd.insp.left')}
            incrementLabel={t('layoutEd.insp.right')}
            onChange={(v) => props.onMove({ col: v - 1, row: widget.row })}
            className="w-[130px]"
          />
          <Stepper
            value={widget.row + 1}
            min={1}
            max={Math.max(1, grid.rows - widget.size.rows + 1)}
            unit={t('layoutEd.insp.row')}
            ariaLabel={t('layoutEd.insp.row')}
            decrementLabel={t('layoutEd.insp.up')}
            incrementLabel={t('layoutEd.insp.down')}
            onChange={(v) => props.onMove({ col: widget.col, row: v - 1 })}
            className="w-[130px]"
          />
        </div>
      </Field>
      <Field no="p5" label={t('layoutEd.insp.display')}>
        <label className="mb-1 block text-[12px] text-muted-foreground" htmlFor="insp-title">
          {t('layoutEd.insp.titleLabel')}
        </label>
        <input
          id="insp-title"
          type="text"
          value={title}
          placeholder={t('layoutEd.insp.titlePlaceholder')}
          onChange={(e) => props.onOptions({ title: e.target.value.trim() === '' ? undefined : e.target.value })}
          className={inputCls}
        />
        <div className="mt-3 mb-1 text-[12px] text-muted-foreground">{t('layoutEd.insp.icon')}</div>
        <div role="radiogroup" aria-label={t('layoutEd.insp.icon')} className="flex flex-wrap gap-1">
          <button
            type="button"
            role="radio"
            aria-checked={icon === ''}
            aria-label={t('layoutEd.insp.iconNone')}
            title={t('layoutEd.insp.iconNone')}
            onClick={() => props.onOptions({ icon: undefined })}
            className={cn('grid size-8 place-items-center rounded-[2px] border outline-none focus-visible:ring-2 focus-visible:ring-ring', icon === '' ? 'border-foreground bg-inv-bg text-inv-ink' : 'border-border hover:bg-panel-2')}
          >
            <Ban size={15} />
          </button>
          {widgetIconNames.map((n) => (
            <button
              key={n}
              type="button"
              role="radio"
              aria-checked={icon === n}
              aria-label={n}
              title={n}
              onClick={() => props.onOptions({ icon: n })}
              className={cn('grid size-8 place-items-center rounded-[2px] border outline-none focus-visible:ring-2 focus-visible:ring-ring', icon === n ? 'border-foreground bg-inv-bg text-inv-ink' : 'border-border hover:bg-panel-2')}
            >
              <WidgetIcon name={n} size={15} />
            </button>
          ))}
        </div>
        {template === 'text' && (
          <>
            <label className="mt-3 mb-1 block text-[12px] text-muted-foreground" htmlFor="insp-text">
              {t('layoutEd.insp.text')}
            </label>
            <textarea
              id="insp-text"
              rows={3}
              value={typeof options.text === 'string' ? options.text : ''}
              onChange={(e) => props.onOptions({ text: e.target.value })}
              className={cn(inputCls, 'h-auto py-1.5')}
            />
          </>
        )}
        {thresholdTemplates.has(template) && (
          <div className="mt-3">
            <div className="flex items-center justify-between text-[13px]">
              <span id="insp-thr-label">{t('layoutEd.insp.threshold')}</span>
              <Switch checked={thr.enabled} onChange={(v) => setThreshold({ enabled: v })} ariaLabel={t('layoutEd.insp.threshold')} />
            </div>
            {thr.enabled ? (
              <div className="mt-2 flex flex-col gap-2">
                <Segmented
                  ariaLabel={t('layoutEd.insp.direction')}
                  className="w-full"
                  options={[
                    { value: 'above', label: t('layoutEd.insp.above'), title: t('layoutEd.insp.aboveTitle') },
                    { value: 'below', label: t('layoutEd.insp.below'), title: t('layoutEd.insp.belowTitle') },
                  ]}
                  value={thr.direction}
                  onChange={(v) => setThreshold({ direction: v })}
                />
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <div className="mb-1 text-[11.5px] text-muted-foreground">{t('layoutEd.insp.warning')}</div>
                    <NumberField key={`${widget.id}-w`} label={t('layoutEd.insp.warning')} value={thr.warning} onCommit={(v) => setThreshold({ warning: v })} />
                  </div>
                  <div>
                    <div className="mb-1 text-[11.5px] text-muted-foreground">{t('layoutEd.insp.critical')}</div>
                    <NumberField key={`${widget.id}-c`} label={t('layoutEd.insp.critical')} value={thr.critical} onCommit={(v) => setThreshold({ critical: v })} />
                  </div>
                </div>
              </div>
            ) : (
              <p className="mt-1.5 text-[11.5px] leading-[1.55] text-muted-foreground">{t('layoutEd.insp.thresholdOff')}</p>
            )}
          </div>
        )}
      </Field>
      <div className="px-3.5 py-3">
        <button
          type="button"
          onClick={props.onRemove}
          className="inline-flex h-9 w-full items-center gap-2 rounded-[2px] border border-status-crit px-3 text-[13px] text-status-crit outline-none hover:bg-status-crit/10 focus-visible:ring-2 focus-visible:ring-ring"
        >
          <Trash2 size={15} />
          {t('layoutEd.insp.remove')}
          <kbd className="ml-auto rounded-[2px] border border-current px-1 font-mono text-[10.5px]">del</kbd>
        </button>
      </div>
    </div>
  )
}
