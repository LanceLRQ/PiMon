import type { Layout } from '@/types/generated'
import { widgetLabel } from './changes'

// 版本预览用的缩略图：按网格比例画出每个 screen 的小组件占位与名称，不取数据
export function LayoutMiniMap({ layout }: { layout: Layout }) {
  const { cols, rows } = layout.grid
  return (
    <div className="flex flex-col gap-3" data-testid="mini-map">
      {layout.screens.map((s) => (
        <figure key={s.id} className="m-0">
          <figcaption className="mb-1 text-[12px]">
            <span className="font-mono text-[11px] text-muted-foreground">{s.id}</span> {s.name}
          </figcaption>
          <div className="relative w-full border border-line-strong bg-panel-2" style={{ aspectRatio: `${cols} / ${rows}` }}>
            {s.widgets.map((w) => (
              <div
                key={w.id}
                data-mini-widget={w.id}
                title={widgetLabel(w)}
                className="absolute overflow-hidden border border-foreground/40 bg-card px-1 text-[10.5px] leading-[1.3]"
                style={{
                  left: `${(w.col / cols) * 100}%`,
                  top: `${(w.row / rows) * 100}%`,
                  width: `${(w.size.cols / cols) * 100}%`,
                  height: `${(w.size.rows / rows) * 100}%`,
                }}
              >
                {widgetLabel(w)}
              </div>
            ))}
          </div>
        </figure>
      ))}
    </div>
  )
}
