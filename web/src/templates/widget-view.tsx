import { memo } from 'react'
import { getTemplateDef } from './registry'
import type { TemplateProps } from './types'

/** 按 widget.template 选模板渲染；屏幕端、编辑器画布与缩略图共用这个入口 */
function WidgetViewImpl(props: TemplateProps) {
  const Component = getTemplateDef(props.widget.template).component
  return <Component {...props} />
}

export const WidgetView = memo(WidgetViewImpl)
