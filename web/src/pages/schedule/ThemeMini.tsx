import type { CSSProperties } from 'react'
import type { ThemeId } from '@/themes'
// 三套主题 token 只在用到屏幕渲染的模块引入，不进 main.tsx（管理端与屏幕端共用 index.html）
import '@/themes/index.css'

// 主题卡缩略图（Ruling 14）：用主题 L2 token 实时渲染的迷你首页，不用图片。
// 间距、圆角、字号取主题 token 乘以缩小系数，所以密度、圆角、字体的差异和真实屏幕一致。
const K = 0.28
const px = (token: string, k = K): string => `calc(var(${token}) * ${k})`

const card: CSSProperties = {
  background: 'var(--card)',
  color: 'var(--card-foreground)',
  borderRadius: px('--radius-card'),
  borderWidth: 'var(--border-card-width)',
  borderStyle: 'var(--border-card-style)',
  borderColor: 'var(--border)',
  boxShadow: 'var(--effect-card-shadow)',
  padding: px('--card-pad'),
}

const label: CSSProperties = { fontFamily: 'var(--font-label)', fontSize: px('--size-label', 0.62), color: 'var(--muted-foreground)', lineHeight: 1.2 }
const value = (size: string): CSSProperties => ({ fontFamily: 'var(--font-numeric)', fontSize: px(size, 0.34), lineHeight: 1.05, fontWeight: 600 })

function Bar({ pct, color }: { pct: number; color: string }) {
  return (
    <div style={{ height: 3, borderRadius: 2, background: 'var(--muted)', overflow: 'hidden', marginTop: 3 }}>
      <div style={{ width: `${pct}%`, height: '100%', background: color }} />
    </div>
  )
}

function Dot({ color }: { color: string }) {
  return <i style={{ display: 'inline-block', width: 5, height: 5, borderRadius: 5, background: color }} />
}

/** 迷你首页：时钟、三个仪表（正常/警告/严重）、一张小趋势；只有数字与色块，不含需要翻译的文字 */
export function ThemeMini({ themeId, reduceEffects = false }: { themeId: ThemeId; reduceEffects?: boolean }) {
  return (
    <div
      data-theme={themeId}
      data-reduce-effects={reduceEffects ? '' : undefined}
      aria-hidden
      className="pointer-events-none w-full overflow-hidden"
      style={{ aspectRatio: '16 / 9', background: 'var(--background)', color: 'var(--foreground)', padding: px('--grid-gap', 0.6), fontFamily: 'var(--font-body)' }}
    >
      <div className="grid h-full w-full grid-cols-3 grid-rows-[1.15fr_1fr]" style={{ gap: px('--grid-gap') }}>
        <div className="col-span-2 flex flex-col justify-center" style={card}>
          <div style={value('--size-value-xl')}>22:47</div>
          <div style={label}>Wed 10/01</div>
        </div>
        <div className="flex flex-col justify-center" style={card}>
          <div style={{ ...value('--size-value-lg'), color: 'var(--primary)' }}>26°</div>
          <div style={label}>
            <Dot color="var(--status-ok-color)" /> 12 ~ 28
          </div>
        </div>
        {[
          { v: '42%', c: 'var(--status-ok-color)', p: 42 },
          { v: '73%', c: 'var(--status-warning-color)', p: 73 },
          { v: '95%', c: 'var(--status-critical-color)', p: 95 },
        ].map((g) => (
          <div key={g.v} className="flex flex-col justify-center" style={card}>
            <div style={{ ...value('--size-value-md'), color: g.c }}>{g.v}</div>
            <Bar pct={g.p} color={g.c} />
          </div>
        ))}
      </div>
    </div>
  )
}

/** 四色小色块：背景、卡片、文字、强调色，用于时段列表与时间轴图例 */
export function ThemeSwatch({ themeId }: { themeId: ThemeId }) {
  return (
    <span data-theme={themeId} aria-hidden className="inline-flex shrink-0 overflow-hidden rounded-[2px] border border-line-strong">
      {(['bg-s-bg', 'bg-s-card', 'bg-s-fg', 'bg-s-primary'] as const).map((c) => (
        <i key={c} className={`h-3.5 w-2.5 ${c}`} />
      ))}
    </span>
  )
}

/** 关屏时段的斜纹底，时间轴与图例共用 */
export const OFF_STRIPES: CSSProperties = {
  background: 'repeating-linear-gradient(135deg, #141414 0 6px, #1f1f1f 6px 12px)',
  color: '#bdb8af',
}
