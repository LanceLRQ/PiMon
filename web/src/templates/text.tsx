import type { CSSProperties } from 'react'
import { useTranslation } from 'react-i18next'
import { WidgetFrame } from './frame'
import { layoutVariant } from './size'
import type { TemplateProps } from './types'
import { fitStyle, useFitText } from './use-fit-text'

// text 模板：显示 options.text 的纯文本（不解析标记）。结构契约：
//   .tpl-text > .tpl-text__body[data-lines]（whitespace-pre-line + 行数截断）
// 数据在前端合成（core 插件），不绑数据项；statusMode=static，不随实例状态灰显。
// 尺寸：2x1 与 4x1 为 2 行，2x2、4x2 为 5 行，3 行高为 8 行（行数 = 3 × 行高 - 1，给标题留位置）；
// 超出行数用省略号截断，字号放不下时在主题字号内缩小。空正文显示「未设置文本」。

export function TextTemplate({ widget, data }: TemplateProps) {
  const { t } = useTranslation()
  const variant = layoutVariant(widget.size)
  const raw = typeof widget.options.text === 'string' ? widget.options.text : ''
  const lines = Math.max(2, widget.size.rows * 3 - 1)
  const [fitRef, fitPx] = useFitText<HTMLDivElement>(raw, { max: 28, min: 11, maxLines: lines })
  const clamp: CSSProperties = {
    display: '-webkit-box',
    WebkitLineClamp: lines,
    WebkitBoxOrient: 'vertical',
    overflow: 'hidden',
    ...fitStyle('--size-value-sm', fitPx),
  }
  return (
    <WidgetFrame widget={widget} data={data} statusMode="static">
      <div ref={fitRef} className="tpl-text h-full min-w-0" data-variant={variant}>
        <div
          className={`tpl-text__body whitespace-pre-line break-words text-[length:var(--size-value-sm)] ${raw ? 'text-s-fg' : 'text-s-muted-fg'}`}
          data-lines={lines}
          style={clamp}
        >
          {raw || t('screenWidget.textEmpty')}
        </div>
      </div>
    </WidgetFrame>
  )
}
