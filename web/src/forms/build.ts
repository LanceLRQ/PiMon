import { checkUrl } from './url'
import { parseDurationSeconds } from './duration'
import {
  joinPath,
  type ErrorMap,
  type Field,
  type FormValues,
  type KvEntry,
  type ListItem,
  type LookupValue,
  type ObjectRow,
  type SecretValue,
  newRowId,
} from './model'

// ---- 载入：服务端配置 -> 表单状态 ----

interface InitOptions {
  // 配置损坏或密钥无法解密：旧的「已设置」标记不再可信，全部按未设置处理
  refill?: boolean
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

// 回显的密钥标记：{"set":true} 或 object_list 内的 {"set":true,"ref":n}
function secretMarker(raw: unknown, refill: boolean | undefined): SecretValue {
  if (!refill && isRecord(raw) && raw.set === true) {
    return { text: '', set: true, ref: typeof raw.ref === 'number' ? raw.ref : undefined }
  }
  return { text: '', set: false }
}

// lookup 已选值的显示文字：值是 JSON 字符串或对象时取 name，否则原样显示
export function lookupLabel(raw: unknown): string {
  let v: unknown = raw
  if (typeof raw === 'string') {
    try {
      v = JSON.parse(raw)
    } catch {
      return raw
    }
  }
  if (isRecord(v) && typeof v.name === 'string' && v.name !== '') return v.name
  return typeof raw === 'string' ? raw : JSON.stringify(raw)
}

function str(raw: unknown, fallback: unknown): string {
  if (typeof raw === 'string') return raw
  return typeof fallback === 'string' ? fallback : ''
}

export function initialValue(f: Field, raw: unknown, opts: InitOptions): unknown {
  switch (f.type) {
    case 'number': {
      const v = typeof raw === 'number' ? raw : f.default
      return typeof v === 'number' ? String(v) : ''
    }
    case 'boolean':
      return typeof raw === 'boolean' ? raw : f.default === true
    case 'enum': {
      return str(raw, f.default)
    }
    case 'secret':
    case 'secret_url':
      return secretMarker(raw, opts.refill)
    case 'lookup':
      return raw === undefined || raw === null || raw === '' ? null : ({ value: raw, label: lookupLabel(raw) } satisfies LookupValue)
    case 'list': {
      const v = Array.isArray(raw) ? raw : Array.isArray(f.default) ? f.default : []
      return v.filter((x): x is string => typeof x === 'string').map((text): ListItem => ({ id: newRowId(), text }))
    }
    case 'kv': {
      if (!isRecord(raw)) return [] as KvEntry[]
      return Object.entries(raw).map(
        ([key, v]): KvEntry => ({
          id: newRowId(),
          key,
          origKey: key,
          value: !f.secret_values && typeof v === 'string' ? v : '',
          secret: f.secret_values ? secretMarker(v, opts.refill) : { text: '', set: false },
        }),
      )
    }
    case 'object_list': {
      const rows = Array.isArray(raw) ? raw : []
      return rows.map((r): ObjectRow => ({ id: newRowId(), values: initialValues(f.fields ?? [], isRecord(r) ? r : {}, opts) }))
    }
    default:
      return str(raw, f.default)
  }
}

export function initialValues(fields: Field[], config: Record<string, unknown> | null | undefined, opts: InitOptions = {}): FormValues {
  const out: FormValues = {}
  for (const f of fields) out[f.key] = initialValue(f, config?.[f.key], opts)
  return out
}

// ---- 显隐 ----

function looseEqual(a: unknown, b: unknown): boolean {
  if (typeof a === 'number' || typeof b === 'number') return typeof a === 'number' && typeof b === 'number' && a === b
  if (typeof a === 'string' || typeof a === 'boolean') return a === b
  return false
}

// visible_when：全部条件成立才显示；条件字段按同级「有效值」判断，没有有效值即不成立
export function isVisible(f: Field, effective: Record<string, unknown>): boolean {
  for (const c of f.visible_when ?? []) {
    if (!(c.key in effective)) return false
    const cur = effective[c.key]
    if (!c.values.some((w) => looseEqual(cur, w))) return false
  }
  return true
}

// ---- 提交：表单状态 -> 配置，同时做即时校验（规则与服务端 schema.Prepare 一致） ----

const lengthOf = (s: string) => [...s].length

function outOfRange(f: Field, n: number): boolean {
  return (f.min !== undefined && n < f.min) || (f.max !== undefined && n > f.max)
}

function compiled(pattern: string | undefined): RegExp | null {
  if (!pattern) return null
  try {
    return new RegExp(pattern)
  } catch {
    // 后端使用 RE2，个别写法 JS 不认；此时交给服务端校验
    return null
  }
}

function emptyValue(v: unknown): boolean {
  if (v === undefined || v === null || v === '') return true
  if (Array.isArray(v)) return v.length === 0
  if (isRecord(v)) return Object.keys(v).length === 0
  return false
}

interface Built {
  // 有效值：参与 visible_when 与必填判断（已保存的密钥也算有值）
  effective: unknown
  // 提交值；undefined 表示不提交该字段
  send: unknown
}

const skip: Built = { effective: undefined, send: undefined }

function buildField(f: Field, raw: unknown, path: string, inRow: boolean, errs: ErrorMap): Built {
  const fail = (code: string): Built => {
    errs[path] = code
    return skip
  }
  switch (f.type) {
    case 'string':
    case 'text': {
      const s = typeof raw === 'string' ? raw : ''
      if (s === '') return skip
      if (outOfRange(f, lengthOf(s))) return fail('out_of_range')
      const re = compiled(f.pattern)
      if (re && !re.test(s)) return fail('pattern_mismatch')
      return { effective: s, send: s }
    }
    case 'number': {
      const s = typeof raw === 'string' ? raw.trim() : ''
      if (s === '') return skip
      const n = Number(s)
      if (!Number.isFinite(n)) return fail('invalid')
      if (outOfRange(f, n)) return fail('out_of_range')
      return { effective: n, send: n }
    }
    case 'boolean':
      return { effective: raw === true, send: raw === true }
    case 'enum': {
      const s = typeof raw === 'string' ? raw : ''
      if (s === '') return skip
      if (!f.options?.some((o) => o.value === s)) return fail('invalid')
      return { effective: s, send: s }
    }
    case 'proxy': {
      const s = typeof raw === 'string' ? raw : ''
      return s === '' ? skip : { effective: s, send: s }
    }
    case 'url': {
      const s = typeof raw === 'string' ? raw.trim() : ''
      if (s === '') return skip
      if (!checkUrl(s, f.allow_query === true, f.allow_public_http === true)) return fail('invalid')
      return { effective: s, send: s }
    }
    case 'duration': {
      const s = typeof raw === 'string' ? raw.trim() : ''
      if (s === '') return skip
      const sec = parseDurationSeconds(s)
      if (sec === null) return fail('invalid')
      if (outOfRange(f, sec)) return fail('out_of_range')
      return { effective: s, send: s }
    }
    case 'secret':
    case 'secret_url': {
      const sv = (raw as SecretValue | undefined) ?? { text: '', set: false }
      if (sv.text !== '') {
        if (f.type === 'secret') {
          if (outOfRange(f, lengthOf(sv.text))) return fail('out_of_range')
          const re = compiled(f.pattern)
          if (re && !re.test(sv.text)) return fail('pattern_mismatch')
        } else if (!checkUrl(sv.text.trim(), true, false)) {
          return fail('invalid')
        }
        return { effective: sv.text, send: f.type === 'secret_url' ? sv.text.trim() : sv.text }
      }
      if (!sv.set) return skip
      // 已保存且留空：顶层不提交该字段，object_list 内带原下标让服务端找回
      if (inRow) return { effective: true, send: sv.ref === undefined ? undefined : { set: true, ref: sv.ref } }
      return { effective: true, send: undefined }
    }
    case 'lookup': {
      const lv = raw as LookupValue | null | undefined
      if (!lv || emptyValue(lv.value)) return skip
      return { effective: lv.value, send: lv.value }
    }
    case 'list': {
      const items = ((raw as ListItem[] | undefined) ?? []).map((i) => i.text.trim()).filter((s) => s !== '')
      if (items.length === 0) return skip
      if (outOfRange(f, items.length)) return fail('out_of_range')
      const re = compiled(f.pattern)
      let ok = true
      items.forEach((s, i) => {
        if (re && !re.test(s)) {
          errs[`${path}[${i}]`] = 'pattern_mismatch'
          ok = false
        }
      })
      return ok ? { effective: items, send: items } : skip
    }
    case 'kv':
      return buildKv(f, (raw as KvEntry[] | undefined) ?? [], path, errs)
    case 'object_list': {
      const rows = (raw as ObjectRow[] | undefined) ?? []
      if (rows.length === 0) return skip
      if (outOfRange(f, rows.length)) return fail('out_of_range')
      const before = Object.keys(errs).length
      const out = rows.map((r, i) => buildLevel(f.fields ?? [], r.values, `${path}[${i}]`, true, errs))
      return Object.keys(errs).length === before ? { effective: out, send: out } : skip
    }
  }
  return fail('invalid')
}

function buildKv(f: Field, entries: KvEntry[], path: string, errs: ErrorMap): Built {
  const out: Record<string, unknown> = {}
  const secretOnly = f.secret_values === true
  let ok = true
  const seen = new Set<string>()
  for (const e of entries) {
    const key = e.key.trim()
    const hasValue = secretOnly ? e.secret.text !== '' || e.secret.set : e.value !== ''
    if (key === '' && !hasValue) continue
    if (key === '') {
      errs[path] = 'invalid'
      ok = false
      continue
    }
    if (seen.has(key)) {
      errs[`${path}.${key}`] = 'duplicate'
      ok = false
      continue
    }
    seen.add(key)
    if (!secretOnly) {
      out[key] = e.value
    } else if (e.secret.text !== '') {
      out[key] = e.secret.text
    } else if (e.secret.set && key === e.origKey) {
      out[key] = { set: true }
    } else if (e.secret.set) {
      // 改了键名：旧值按键名保存，无法随新键名带过去，必须重新填写
      errs[`${path}.${key}`] = 'required'
      ok = false
    } else {
      // 新键没有旧值可保留，值留空等于没填
      errs[`${path}.${key}`] = 'required'
      ok = false
    }
  }
  const n = Object.keys(out).length
  if (n === 0) return skip
  if (outOfRange(f, n)) {
    errs[path] = 'out_of_range'
    return skip
  }
  return ok ? { effective: out, send: out } : skip
}

function hasErrorUnder(errs: ErrorMap, path: string): boolean {
  return Object.keys(errs).some((k) => k === path || k.startsWith(`${path}.`) || k.startsWith(`${path}[`))
}

function buildLevel(fields: Field[], values: FormValues, prefix: string, inRow: boolean, errs: ErrorMap): Record<string, unknown> {
  const effective: Record<string, unknown> = {}
  const out: Record<string, unknown> = {}
  for (const f of fields) {
    if (!isVisible(f, effective)) continue
    const path = joinPath(prefix, f.key)
    const built = buildField(f, values[f.key], path, inRow, errs)
    let { effective: eff, send } = built
    if (emptyValue(eff)) {
      // 没有输入：沿用 manifest 默认值（服务端同样处理）
      if (hasErrorUnder(errs, path)) continue
      if (!emptyValue(f.default)) {
        eff = f.default
        send = f.default
      } else {
        if (f.required) errs[path] = 'required'
        continue
      }
    }
    effective[f.key] = eff
    if (send !== undefined) out[f.key] = send
  }
  return out
}

export interface BuildResult {
  config: Record<string, unknown>
  errors: ErrorMap
}

// 把表单状态转成提交的配置：隐藏字段不提交，已保存且留空的密钥不提交；
// 同时给出按路径的即时校验错误（与服务端 FieldErrors 同一套错误码）
export function buildConfig(fields: Field[], values: FormValues): BuildResult {
  const errors: ErrorMap = {}
  const config = buildLevel(fields, values, '', false, errors)
  return { config, errors }
}

// 当前显示的字段键：按同级有效值逐个判断 visible_when（不产生错误）
export function visibleKeys(fields: Field[], values: FormValues): Set<string> {
  const effective: Record<string, unknown> = {}
  const shown = new Set<string>()
  for (const f of fields) {
    if (!isVisible(f, effective)) continue
    shown.add(f.key)
    let eff = buildField(f, values[f.key], f.key, false, {}).effective
    if (emptyValue(eff)) eff = emptyValue(f.default) ? undefined : f.default
    if (eff !== undefined) effective[f.key] = eff
  }
  return shown
}
