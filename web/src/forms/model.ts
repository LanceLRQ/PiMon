import type { PluginField } from '@/types/generated'

// 自动表单的内部取值形态。表单状态与提交给服务端的配置分开：状态里数字是字符串、
// 密钥带「已设置」标记、键值与对象列表带原下标，提交时由 build.ts 转换。

export type Field = PluginField

export type FormValues = Record<string, unknown>

// 密钥：text 为本次输入；set 表示服务端已保存过值（留空即保留）；
// ref 是 object_list 元素内密钥回显的原下标，编辑、重排时原样回传
export interface SecretValue {
  text: string
  set: boolean
  ref?: number
}

let rowSeq = 0
// 列表、键值、对象列表的行需要稳定 id，重排或删行后行内控件状态才不会错位
export function newRowId(): string {
  return `row${++rowSeq}`
}

export interface ListItem {
  id: string
  text: string
}

export interface KvEntry {
  id: string
  key: string
  // 非密钥值
  value: string
  // secret_values 的值
  secret: SecretValue
  // 载入时的键名；改名后旧值无法按键找回，须重新填写
  origKey: string
}

export interface ObjectRow {
  id: string
  values: FormValues
}

// lookup 字段：value 原样提交，label 只用于显示
export interface LookupValue {
  value: unknown
  label: string
}

// 路径 -> 错误码（required、invalid、out_of_range、pattern_mismatch、duplicate）。
// 路径形如 url、headers.X-Token、accounts[1].token，与服务端 FieldErrors 一致
export type ErrorMap = Record<string, string>

export function joinPath(prefix: string, key: string): string {
  return prefix === '' ? key : `${prefix}.${key}`
}

// 取某路径及其子路径上的第一个错误
export function errorAt(errors: ErrorMap, path: string): string | undefined {
  if (errors[path]) return errors[path]
  for (const k of Object.keys(errors)) {
    if (k.startsWith(`${path}[`) || k.startsWith(`${path}.`)) return errors[k]
  }
  return undefined
}

// 清掉某路径及其子路径上的错误
export function clearErrors(errors: ErrorMap, path: string): ErrorMap {
  const out: ErrorMap = {}
  let changed = false
  for (const [k, v] of Object.entries(errors)) {
    if (k === path || k.startsWith(`${path}[`) || k.startsWith(`${path}.`)) changed = true
    else out[k] = v
  }
  return changed ? out : errors
}

// 所有输入组件共用的属性
export interface ControlProps<V = unknown> {
  field: Field
  value: V
  onChange: (value: V) => void
  // 配置路径（含 object_list 内嵌路径），用于错误定位
  path: string
  // DOM id，行标签通过它关联控件
  id: string
  invalid: boolean
  describedBy?: string
}
