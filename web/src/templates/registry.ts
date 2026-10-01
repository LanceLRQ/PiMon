import type { ComponentType } from 'react'
import type { WidgetSize } from '@/types/generated'
import { ClockTemplate } from './clock'
import { GaugeTemplate } from './gauge'
import { PendingTemplate } from './pending'
import { StateTemplate } from './state'
import { TextTemplate } from './text'
import type { TemplateProps } from './types'
import { ValueTemplate } from './value'

export interface TemplateDef {
  name: string
  component: ComponentType<TemplateProps>
  /** 支持的尺寸，与 Go 侧目录对照（registry.test.tsx） */
  sizes: readonly WidgetSize[]
  /** 结构契约摘要；完整说明在各模板文件顶部注释 */
  contract: string
  implemented: boolean
}

const sz = (cols: number, rows: number): WidgetSize => ({ cols, rows })

// 名字集合须与 Go 侧 manifest.Templates 一致（registry.test.tsx 对照）。
// 暂未实现的模板挂 PendingTemplate，实现时替换 component、sizes 并置 implemented。
function pending(name: string): TemplateDef {
  return { name, component: PendingTemplate, sizes: [], contract: '尚未实现', implemented: false }
}

export const templateDefs: readonly TemplateDef[] = [
  { name: 'value', component: ValueTemplate, sizes: [sz(1, 1), sz(2, 1), sz(2, 2)], implemented: true,
    contract: '.tpl-value > .tpl-value__reading > number/unit；money 由 value 承担' },
  { name: 'gauge', component: GaugeTemplate, sizes: [sz(1, 1), sz(2, 1), sz(2, 2)], implemented: true,
    contract: '.tpl-gauge > [role=meter] + .tpl-gauge__reading' },
  { name: 'state', component: StateTemplate, sizes: [sz(1, 1), sz(2, 1)], implemented: true,
    contract: '.tpl-state > .tpl-state__marker + .tpl-state__label' },
  pending('status-grid'),
  pending('list'),
  pending('table'),
  pending('chart'),
  { name: 'clock', component: ClockTemplate, sizes: [sz(1, 1), sz(2, 1), sz(4, 2)], implemented: true,
    contract: '.tpl-clock > .tpl-clock__time（hour:minute[:second]）+ .tpl-clock__date' },
  pending('weather'),
  { name: 'text', component: TextTemplate, sizes: [sz(2, 1), sz(2, 2), sz(4, 1), sz(4, 2)], implemented: true,
    contract: '.tpl-text > .tpl-text__body[data-lines]' },
  pending('quota'),
  pending('quota-multi'),
]

export const templateNames: readonly string[] = templateDefs.map((d) => d.name)

const byName = new Map(templateDefs.map((d) => [d.name, d]))

/** 未知模板名也返回占位定义，渲染端无需再判空 */
export function getTemplateDef(name: string): TemplateDef {
  return byName.get(name) ?? pending(name)
}

export function isImplemented(name: string): boolean {
  return byName.get(name)?.implemented === true
}
