import { describe, expect, it } from 'vitest'
import { buildConfig, initialValues, lookupLabel } from './build'
import { parseDurationSeconds, formatDurationSeconds } from './duration'
import type { Field } from './model'
import { checkUrl } from './url'

const li = (texts: string[]) => texts.map((text, i) => ({ id: `t${i}`, text }))
const f = (over: Partial<Field> & Pick<Field, 'key' | 'type'>): Field => ({ title: over.key, required: false, ...over })

describe('字段类型的初始值与提交值', () => {
  it('string / text / number / boolean / enum / duration / proxy', () => {
    const fields: Field[] = [
      f({ key: 's', type: 'string' }),
      f({ key: 't', type: 'text' }),
      f({ key: 'n', type: 'number', default: 5 }),
      f({ key: 'b', type: 'boolean', default: true }),
      f({ key: 'e', type: 'enum', options: [{ value: 'GET', title: 'GET' }, { value: 'HEAD', title: 'HEAD' }], default: 'GET' }),
      f({ key: 'd', type: 'duration', default: '10s' }),
      f({ key: 'p', type: 'proxy' }),
    ]
    const init = initialValues(fields, undefined)
    expect(init).toMatchObject({ s: '', t: '', n: '5', b: true, e: 'GET', d: '10s', p: '' })
    const { config, errors } = buildConfig(fields, { ...init, s: 'abc', t: '多行\n文本', n: '42', p: 'px1' })
    expect(errors).toEqual({})
    expect(config).toEqual({ s: 'abc', t: '多行\n文本', n: 42, b: true, e: 'GET', d: '10s', p: 'px1' })
  })

  it('直连代理不提交 proxy 字段', () => {
    const fields = [f({ key: 'p', type: 'proxy' })]
    expect(buildConfig(fields, { p: '' }).config).toEqual({})
  })

  it('必填缺失、范围、pattern、枚举外值给出与服务端相同的错误码', () => {
    const fields: Field[] = [
      f({ key: 'a', type: 'string', required: true }),
      f({ key: 'n', type: 'number', min: 1, max: 10 }),
      f({ key: 'p', type: 'string', pattern: '^[a-z]+$' }),
      f({ key: 'd', type: 'duration', min: 1, max: 25 }),
      f({ key: 'bad', type: 'number' }),
    ]
    const { errors } = buildConfig(fields, { a: '', n: '11', p: 'ABC', d: '1m', bad: 'x1' })
    expect(errors).toEqual({ a: 'required', n: 'out_of_range', p: 'pattern_mismatch', d: 'out_of_range', bad: 'invalid' })
  })

  it('空值回落到 manifest 默认值，且默认值参与 visible_when', () => {
    const fields: Field[] = [
      f({ key: 'mode', type: 'string', default: 'x' }),
      f({ key: 'extra', type: 'string', visible_when: [{ key: 'mode', values: ['x'] }] }),
    ]
    const { config } = buildConfig(fields, { mode: '', extra: 'v' })
    expect(config).toEqual({ mode: 'x', extra: 'v' })
  })
})

describe('visible_when', () => {
  const fields: Field[] = [
    f({ key: 'provider', type: 'enum', options: [{ value: 'a', title: 'a' }, { value: 'b', title: 'b' }], default: 'a' }),
    f({ key: 'api_key', type: 'secret', required: true, visible_when: [{ key: 'provider', values: ['b'] }] }),
    f({ key: 'flag', type: 'boolean', default: false }),
    f({ key: 'deep', type: 'string', visible_when: [{ key: 'flag', values: [true] }] }),
  ]

  it('条件不成立的字段不提交也不校验必填', () => {
    const init = initialValues(fields, undefined)
    const { config, errors } = buildConfig(fields, { ...init, deep: 'secret-leftover' })
    expect(errors).toEqual({})
    expect(config).toEqual({ provider: 'a', flag: false })
  })

  it('条件成立后显示并校验', () => {
    const init = initialValues(fields, undefined)
    const { config, errors } = buildConfig(fields, { ...init, provider: 'b', flag: true, deep: 'ok' })
    expect(errors).toEqual({ api_key: 'required' })
    expect(config).toMatchObject({ provider: 'b', flag: true, deep: 'ok' })
  })
})

describe('密钥', () => {
  const fields: Field[] = [f({ key: 'token', type: 'secret', required: true }), f({ key: 'hook', type: 'secret_url' })]

  it('已设置且留空时请求体里没有该字段，必填也满足', () => {
    const init = initialValues(fields, { token: { set: true }, hook: { set: true } })
    const { config, errors } = buildConfig(fields, init)
    expect(errors).toEqual({})
    expect(config).toEqual({})
  })

  it('输入新值则提交新值', () => {
    const init = initialValues(fields, { token: { set: true } }) as Record<string, { text: string }>
    init.token = { ...init.token, text: 'new-token' } as never
    expect(buildConfig(fields, init).config).toEqual({ token: 'new-token' })
  })

  it('未设置且留空：必填报 required', () => {
    expect(buildConfig(fields, initialValues(fields, {})).errors).toEqual({ token: 'required' })
  })

  it('配置损坏需重填：已设置标记被忽略', () => {
    const init = initialValues(fields, { token: { set: true } }, { refill: true })
    expect(buildConfig(fields, init).errors).toEqual({ token: 'required' })
  })

  it('secret_url 校验地址形态', () => {
    const init = initialValues(fields, { token: { set: true } }) as Record<string, unknown>
    init.hook = { text: 'ftp://x', set: false }
    expect(buildConfig(fields, init).errors).toEqual({ hook: 'invalid' })
  })
})

describe('url', () => {
  it('按字段选项校验查询参数与公网 http', () => {
    expect(checkUrl('https://a.example/x?y=1', true, false)).toBe(true)
    expect(checkUrl('https://a.example/x?y=1', false, false)).toBe(false)
    expect(checkUrl('http://a.example', false, false)).toBe(false)
    expect(checkUrl('http://a.example', false, true)).toBe(true)
    expect(checkUrl('http://192.168.1.2:8080', false, false)).toBe(true)
    expect(checkUrl('http://nas.local', false, false)).toBe(true)
    expect(checkUrl('https://u:p@a.example', true, true)).toBe(false)
    expect(checkUrl('ftp://a.example', true, true)).toBe(false)
    expect(checkUrl('not a url', true, true)).toBe(false)
  })

  it('url 字段', () => {
    const fields = [f({ key: 'u', type: 'url', required: true, allow_query: false })]
    expect(buildConfig(fields, { u: ' https://a.example ' }).config).toEqual({ u: 'https://a.example' })
    expect(buildConfig(fields, { u: 'https://a.example/?q=1' }).errors).toEqual({ u: 'invalid' })
    expect(buildConfig(fields, { u: '' }).errors).toEqual({ u: 'required' })
  })
})

describe('duration', () => {
  it('解析与后端一致的写法', () => {
    expect(parseDurationSeconds('10s')).toBe(10)
    expect(parseDurationSeconds('1m30s')).toBe(90)
    expect(parseDurationSeconds('1.5h')).toBe(5400)
    expect(parseDurationSeconds('300ms')).toBeCloseTo(0.3)
    expect(parseDurationSeconds('10')).toBeNull()
    expect(parseDurationSeconds('')).toBeNull()
    expect(parseDurationSeconds('-5s')).toBeNull()
    expect(parseDurationSeconds('5x')).toBeNull()
    expect(formatDurationSeconds(90)).toBe('1m30s')
    expect(formatDurationSeconds(3600)).toBe('1h')
  })
})

describe('list / kv', () => {
  it('list：丢弃空项、逐项 pattern 校验并给出下标路径', () => {
    const fields = [f({ key: 'hosts', type: 'list', pattern: '^[a-z]+$' })]
    expect(buildConfig(fields, { hosts: li(['a', ' ', 'b']) }).config).toEqual({ hosts: ['a', 'b'] })
    expect(buildConfig(fields, { hosts: li(['a', 'B1']) }).errors).toEqual({ 'hosts[1]': 'pattern_mismatch' })
  })

  it('kv 密钥值：未改保留标记、改键名要求重填、新输入提交明文', () => {
    const fields = [f({ key: 'headers', type: 'kv', secret_values: true })]
    const init = initialValues(fields, { headers: { Authorization: { set: true }, 'X-A': { set: true } } }) as {
      headers: { key: string; secret: { text: string; set: boolean } }[]
    }
    expect(buildConfig(fields, init).config).toEqual({ headers: { Authorization: { set: true }, 'X-A': { set: true } } })

    const renamed = structuredClone(init)
    renamed.headers[0].key = 'Auth2'
    expect(buildConfig(fields, renamed).errors).toEqual({ 'headers.Auth2': 'required' })

    const typed = structuredClone(init)
    typed.headers[1].secret.text = 'plain'
    expect(buildConfig(fields, typed).config).toEqual({ headers: { Authorization: { set: true }, 'X-A': 'plain' } })
  })

  it('kv 普通值与重复键', () => {
    const fields = [f({ key: 'labels', type: 'kv' })]
    const init = [
      { id: 'a', key: 'a', value: '1', secret: { text: '', set: false }, origKey: '' },
      { id: 'b', key: ' a ', value: '2', secret: { text: '', set: false }, origKey: '' },
    ]
    expect(buildConfig(fields, { labels: init }).errors).toEqual({ 'labels.a': 'duplicate' })
    expect(buildConfig(fields, { labels: [init[0], { id: 'c', key: '', value: '', secret: { text: '', set: false }, origKey: '' }] }).config).toEqual({
      labels: { a: '1' },
    })
  })
})

describe('kv 新键与 enum', () => {
  it('密钥 kv 新键值留空：没有旧值可保留，报该键 required', () => {
    const fields = [f({ key: 'headers', type: 'kv', secret_values: true })]
    const entry = { id: 'x', key: 'X-New', value: '', origKey: '', secret: { text: '', set: false } }
    expect(buildConfig(fields, { headers: [entry] }).errors).toEqual({ 'headers.X-New': 'required' })
  })

  it('必填且没有默认值的 enum 不自动选第一项，缺省即 required', () => {
    const fields = [f({ key: 'e', type: 'enum', required: true, options: [{ value: 'a', title: 'a' }] })]
    const init = initialValues(fields, undefined)
    expect(init.e).toBe('')
    expect(buildConfig(fields, init).errors).toEqual({ e: 'required' })
  })
})

describe('object_list', () => {
  const fields: Field[] = [
    f({
      key: 'accounts',
      type: 'object_list',
      fields: [
        f({ key: 'name', type: 'string', required: true }),
        f({ key: 'token', type: 'secret', required: true }),
        f({ key: 'mode', type: 'enum', options: [{ value: 'a', title: 'a' }, { value: 'b', title: 'b' }], default: 'a' }),
        f({ key: 'extra', type: 'string', visible_when: [{ key: 'mode', values: ['b'] }] }),
      ],
    }),
  ]

  it('元素内已保存的密钥带原下标回传，重排后 ref 不变', () => {
    const init = initialValues(fields, {
      accounts: [
        { name: 'a', token: { set: true, ref: 0 }, mode: 'a' },
        { name: 'b', token: { set: true, ref: 1 }, mode: 'a' },
      ],
    }) as { accounts: unknown[] }
    init.accounts.reverse()
    const { config, errors } = buildConfig(fields, init)
    expect(errors).toEqual({})
    expect(config).toEqual({
      accounts: [
        { name: 'b', token: { set: true, ref: 1 }, mode: 'a' },
        { name: 'a', token: { set: true, ref: 0 }, mode: 'a' },
      ],
    })
  })

  it('元素内错误带 [i].key 路径，隐藏的子字段不提交', () => {
    const init = initialValues(fields, { accounts: [{ name: '', mode: 'a', extra: 'left' }] })
    const { errors } = buildConfig(fields, init)
    expect(errors).toEqual({ 'accounts[0].name': 'required', 'accounts[0].token': 'required' })
    const ok = initialValues(fields, { accounts: [{ name: 'n', mode: 'a', extra: 'left' }] }) as { accounts: { values: Record<string, unknown> }[] }
    ok.accounts[0].values.token = { text: 'tk', set: false }
    expect(buildConfig(fields, ok).config).toEqual({ accounts: [{ name: 'n', token: 'tk', mode: 'a' }] })
  })
})

describe('lookup', () => {
  const fields = [f({ key: 'city', type: 'lookup', required: true })]
  it('提交原始候选值，显示取 name', () => {
    const raw = JSON.stringify({ id: 1, name: '杭州', lat: 30.2, lon: 120.1 })
    const init = initialValues(fields, { city: raw }) as { city: { label: string } }
    expect(init.city.label).toBe('杭州')
    expect(buildConfig(fields, init).config).toEqual({ city: raw })
    expect(lookupLabel('plain')).toBe('plain')
    expect(lookupLabel({ name: 'N' })).toBe('N')
  })
  it('未选择时必填报错', () => {
    expect(buildConfig(fields, initialValues(fields, {})).errors).toEqual({ city: 'required' })
  })
})
