import { Power } from 'lucide-react'
import { useRef, type CSSProperties, type KeyboardEvent, type PointerEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { getTheme, isThemeId, type ThemeId } from '@/themes'
import { DAY, formatHM, lengthOf, OFF, type DraftPeriod } from './model'
import { OFF_STRIPES, ThemeSwatch } from './ThemeMini'

interface Props {
  periods: readonly DraftPeriod[]
  selectedKey: string | null
  onSelect: (key: string) => void
  /** 把某段起点的边界移动 delta 分钟 */
  onMoveBoundary: (key: string, delta: number) => void
  nowMinute: number
  nowLabel: string
  mobile: boolean
}

const STEP = 5
const BIG_STEP = 60

/** 24 小时时间轴：色段点击选中，段与段之间的边界可拖动或用方向键移动；「现在」指针由设置时区算出 */
export function Timeline({ periods, selectedKey, onSelect, onMoveBoundary, nowMinute, nowLabel, mobile }: Props) {
  const { t } = useTranslation()
  const trackRef = useRef<HTMLDivElement>(null)
  const dragRef = useRef<{ key: string; start: number } | null>(null)

  const themeName = (id: string) => (id === OFF ? t('schedule.tl.off') : id)

  const pieces = periods.flatMap((p, i) => {
    const end = p.start + lengthOf(p)
    const spans = end > DAY ? [[p.start, DAY], [0, end - DAY]] : [[p.start, end]]
    return spans.map(([from, to], k) => ({ p, i, from, to, k }))
  })

  const onKey = (e: KeyboardEvent, key: string) => {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
    e.preventDefault()
    const step = e.shiftKey ? BIG_STEP : STEP
    onMoveBoundary(key, e.key === 'ArrowRight' ? step : -step)
  }
  const minuteAt = (clientX: number): number | null => {
    const r = trackRef.current?.getBoundingClientRect()
    if (!r || r.width === 0) return null
    const frac = Math.min(1, Math.max(0, (clientX - r.left) / r.width))
    return Math.round((frac * DAY) / STEP) * STEP
  }
  const onPointerDown = (e: PointerEvent<HTMLButtonElement>, p: DraftPeriod) => {
    e.currentTarget.setPointerCapture?.(e.pointerId)
    dragRef.current = { key: p.key, start: p.start }
  }
  const onPointerMove = (e: PointerEvent<HTMLButtonElement>) => {
    const d = dragRef.current
    if (!d) return
    const m = minuteAt(e.clientX)
    const cur = periods.find((x) => x.key === d.key)
    if (m === null || !cur) return
    const delta = m - cur.start
    if (delta !== 0) onMoveBoundary(d.key, delta)
  }
  const endDrag = () => {
    dragRef.current = null
  }

  const ticks = Array.from({ length: 25 }, (_, h) => h)
  const nowLeft = (nowMinute / DAY) * 100
  const nowAt = `clamp(8px, ${nowLeft}%, calc(100% - 8px))`

  return (
    <div className="px-5 pt-11 pb-3.5 mobile:px-3 mobile:pt-10 mobile:pb-2.5">
      <div ref={trackRef} data-testid="timeline" className="relative h-[76px] rounded-[2px] border border-line-strong mobile:h-16">
        {pieces.map(({ p, i, from, to, k }) => {
          const w = ((to - from) / DAY) * 100
          const off = p.theme === OFF
          const selected = p.key === selectedKey
          const style: CSSProperties = { left: `${(from / DAY) * 100}%`, width: `${w}%`, ...(off ? OFF_STRIPES : {}) }
          const themed = !off && isThemeId(p.theme)
          const name = themeName(p.theme)
          return (
            <button
              key={`${p.key}-${k}`}
              type="button"
              data-period={i}
              data-theme={themed ? p.theme : undefined}
              aria-label={t('schedule.tl.seg', { from: formatHM(p.start), to: formatHM(p.end), theme: name })}
              aria-pressed={selected}
              onClick={() => onSelect(p.key)}
              style={style}
              className={cn(
                'absolute top-0 bottom-0 flex flex-col justify-between overflow-hidden border-r border-line-strong p-2 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring last:border-r-0 mobile:items-center mobile:justify-center mobile:p-1',
                themed && 'bg-s-bg text-s-fg',
                selected && 'ring-2 ring-signal ring-inset',
              )}
            >
              {w >= 8 && (
                <>
                  <span className="font-mono text-[12px] font-medium whitespace-nowrap mobile:hidden">
                    {formatHM(p.start)}–{formatHM(p.end)}
                  </span>
                  <span className="flex items-center gap-1.5 text-[11.5px] whitespace-nowrap opacity-85 mobile:hidden">
                    {off ? <Power size={12} /> : themed ? <ThemeSwatch themeId={p.theme as ThemeId} /> : null}
                    {name}
                  </span>
                  <span className="hidden mobile:flex">{off ? <Power size={13} /> : themed ? <ThemeSwatch themeId={getTheme(p.theme).id} /> : null}</span>
                </>
              )}
            </button>
          )
        })}
        {periods.length >= 2 &&
          periods.map((p) => (
            <button
              key={`h-${p.key}`}
              type="button"
              role="slider"
              aria-label={t('schedule.tl.handle', { time: formatHM(p.start) })}
              aria-valuemin={0}
              aria-valuemax={DAY - 1}
              aria-valuenow={p.start}
              aria-valuetext={formatHM(p.start)}
              onKeyDown={(e) => onKey(e, p.key)}
              onPointerDown={(e) => onPointerDown(e, p)}
              onPointerMove={onPointerMove}
              onPointerUp={endDrag}
              onPointerCancel={endDrag}
              style={{ left: `${(p.start / DAY) * 100}%` }}
              className="group absolute top-0 bottom-0 z-[3] w-3 -translate-x-1/2 cursor-ew-resize touch-none outline-none"
            >
              <span aria-hidden className="mx-auto block h-full w-[3px] bg-transparent group-hover:bg-signal group-focus-visible:bg-signal" />
            </button>
          ))}
        {/* 「现在」指针：线、标签、菱形各自定位在轴内，位置夹在两端留出的余量里，避免午夜前后伸出轴外 */}
        <div aria-hidden className="pointer-events-none absolute -top-[30px] -bottom-2 z-[2] w-0.5 -translate-x-1/2 bg-signal" style={{ left: nowAt }} />
        <span
          className={cn(
            'pointer-events-none absolute -top-[30px] z-[2] bg-signal px-[7px] py-0.5 font-mono text-[11.5px] font-medium whitespace-nowrap text-primary-foreground',
            nowLeft < 12 ? 'rounded-r-[2px]' : '-translate-x-full rounded-l-[2px]',
          )}
          style={{ left: nowLeft < 12 ? `calc(${nowAt} + 1px)` : `calc(${nowAt} - 1px)` }}
        >
          {nowLabel}
        </span>
        <span aria-hidden className="pointer-events-none absolute -bottom-[7px] z-[2] size-2 -translate-x-1/2 rotate-45 bg-signal" style={{ left: nowAt }} />
      </div>
      <div aria-hidden className="relative mt-1.5 h-7">
        {ticks.map((h) => {
          const major = h % (mobile ? 6 : 3) === 0
          return (
            <span key={h}>
              <i className={cn('absolute top-0 w-px bg-line-strong', h % 3 === 0 ? 'h-[9px]' : 'h-[5px]')} style={{ left: `${(h / 24) * 100}%` }} />
              {major && (
                <span className="absolute top-[11px] -translate-x-1/2 font-mono text-[10.5px] text-muted-foreground" style={{ left: `${(h / 24) * 100}%` }}>
                  {String(h).padStart(2, '0')}
                </span>
              )}
            </span>
          )
        })}
      </div>
    </div>
  )
}
