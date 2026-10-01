import { describe, expect, it } from 'vitest'
import { prototypeInstances, makeInstance } from './fixtures'
import {
  attentionStates,
  filterInstances,
  isAttention,
  normalizeStatus,
  parseQuery,
  sortInstances,
  statusRank,
  type TableFilter,
} from './query'

const none: TableFilter = { attention: false, location: null, plugin: null, query: '' }
const names = (list: { name: string }[]) => list.map((i) => i.name)
const run = (query: string, extra: Partial<TableFilter> = {}) =>
  names(filterInstances(prototypeInstances, { ...none, ...extra, query }))

describe('查询语法解析', () => {
  it('取出 status、runs、plugin 与自由文本，键与值不区分大小写', () => {
    expect(parseQuery('Status:WARNING runs:agent plugin:host-metrics nas')).toEqual({
      status: 'warning',
      runs: 'agent',
      plugin: 'host-metrics',
      text: ['nas'],
    })
  })

  it('冒号后没有值时按自由文本处理，空白折叠', () => {
    expect(parseQuery('  status:   a  ')).toEqual({ text: ['status:', 'a'] })
    expect(parseQuery('')).toEqual({ text: [] })
  })

  it('状态值中英文都认', () => {
    expect(normalizeStatus('warn')).toBe('warning')
    expect(normalizeStatus('警告')).toBe('warning')
    expect(normalizeStatus('严重')).toBe('critical')
    expect(normalizeStatus('fail')).toBe('error')
    expect(normalizeStatus('采集失败')).toBe('error')
    expect(normalizeStatus('过期')).toBe('stale')
    expect(normalizeStatus('未知')).toBe('unknown')
    expect(normalizeStatus('不存在')).toBeNull()
  })
})

describe('与原型一致的 6 个示例查询', () => {
  it('status:warning', () => {
    expect(run('status:warning')).toEqual(['Claude', '网络连通'])
  })
  it('status:严重 runs:agent', () => {
    expect(run('status:严重 runs:agent')).toEqual(['ubuntu-srv'])
  })
  it('plugin:host-metrics', () => {
    expect(run('plugin:host-metrics')).toEqual(['ubuntu-srv', '树莓派本机', '飞牛 NAS', 'vps-xray'])
  })
  it('status:ok plugin:ai', () => {
    expect(run('status:ok plugin:ai')).toEqual(['Codex', 'GLM', 'DeepSeek 余额', 'OpenRouter 余额'])
  })
  it('http 博客：自由文本全部命中才算', () => {
    expect(run('http 博客')).toEqual(['个人博客'])
  })
  it('ubuntu-srv：名称与插件 id 都参与自由文本匹配', () => {
    expect(run('ubuntu-srv')).toEqual(['ubuntu-srv', 'ubuntu-srv SSH'])
  })
  it('status:fail 命中采集失败；无法识别的状态值什么都不匹配', () => {
    expect(run('status:fail')).toEqual(['家里 NAS 网页'])
    expect(run('status:bogus')).toEqual([])
  })
  it('runs:hub 与 runs:agent 按类别匹配，其他值按主机标识包含匹配', () => {
    expect(run('runs:hub')).toHaveLength(12)
    expect(run('runs:开发机')).toEqual(['Claude', 'Codex'])
    expect(run('runs:fnos')).toEqual(['飞牛 NAS'])
  })
  it('runs:agent 取运行位置', () => {
    expect(run('runs:agent')).toEqual(['ubuntu-srv', 'Claude', '飞牛 NAS', 'vps-xray', 'Codex'])
  })
  it('自由文本也匹配读数摘要', () => {
    expect(run('证书剩余')).toEqual(['个人博客'])
  })
})

describe('筛选组合', () => {
  it('需关注只留 critical、warning、error、stale', () => {
    expect(run('', { attention: true })).toEqual(['ubuntu-srv', 'Claude', '网络连通', '家里 NAS 网页'])
    expect([...attentionStates].sort()).toEqual(['critical', 'error', 'stale', 'warning'])
    expect(isAttention('ok')).toBe(false)
    expect(isAttention('stale')).toBe(true)
  })

  it('运行位置、插件与查询叠加', () => {
    expect(run('', { location: '开发机' })).toEqual(['Claude', 'Codex'])
    expect(run('', { plugin: 'host-metrics', location: 'hub' })).toEqual(['树莓派本机'])
    expect(run('status:ok', { plugin: 'http-check' })).toEqual(['个人博客'])
    expect(run('', { attention: true, plugin: 'http-check' })).toEqual(['家里 NAS 网页'])
  })

  it('插件筛选是精确匹配，查询里的 plugin: 是包含匹配', () => {
    expect(run('', { plugin: 'ai' })).toEqual([])
    expect(run('plugin:ai').length).toBe(5)
  })
})

describe('排序', () => {
  const sorted = (key: Parameters<typeof sortInstances>[1], dir: 'asc' | 'desc') =>
    names(sortInstances(prototypeInstances, key, dir))

  it('默认按状态严重优先，同状态按名称', () => {
    const list = sorted('status', 'desc')
    expect(list.slice(0, 2)).toEqual(['ubuntu-srv', '家里 NAS 网页'])
    // 同为警告：顺序由排序用的区域比较决定
    expect(list.slice(2, 4)).toEqual(['Claude', '网络连通'].sort((a, b) => a.localeCompare(b, 'zh')))
    const tail = list.slice(4)
    expect(tail).toEqual([...tail].sort((a, b) => a.localeCompare(b, 'zh')))
    expect(statusRank('critical')).toBeGreaterThan(statusRank('error'))
    expect(statusRank('error')).toBeGreaterThan(statusRank('warning'))
    expect(statusRank('warning')).toBeGreaterThan(statusRank('stale'))
    expect(statusRank('stale')).toBeGreaterThan(statusRank('unknown'))
    expect(statusRank('unknown')).toBeGreaterThan(statusRank('ok'))
  })

  it('状态升序时 ok 在前，同状态仍按名称升序', () => {
    const list = sorted('status', 'asc')
    expect(list.at(-1)).toBe('ubuntu-srv')
    const oks = list.slice(0, 13)
    expect(oks).toEqual([...oks].sort((a, b) => a.localeCompare(b, 'zh')))
  })

  it('按名称正反序', () => {
    const asc = sorted('name', 'asc')
    expect(asc).toEqual([...asc].sort((a, b) => a.localeCompare(b, 'zh')))
    expect(sorted('name', 'desc').slice(0, 1)).toEqual(asc.slice(-1))
  })

  it('按更新时间：升序为最近更新在前，从未成功的排最旧', () => {
    const list = [
      makeInstance({ id: 'a', name: 'a', last_success_at: '2026-10-01T11:00:00Z' }),
      makeInstance({ id: 'b', name: 'b', last_success_at: null }),
      makeInstance({ id: 'c', name: 'c', last_success_at: '2026-10-01T11:59:00Z' }),
    ]
    expect(names(sortInstances(list, 'ago', 'asc'))).toEqual(['c', 'a', 'b'])
    expect(names(sortInstances(list, 'ago', 'desc'))).toEqual(['b', 'a', 'c'])
  })

  it('按运行位置', () => {
    const list = sortInstances(prototypeInstances, 'run', 'asc')
    const hosts = list.map((i) => i.runs_on)
    expect(hosts).toEqual([...hosts].sort((a, b) => a.localeCompare(b, 'zh')))
  })

  it('不修改入参', () => {
    const before = names(prototypeInstances)
    sortInstances(prototypeInstances, 'name', 'desc')
    expect(names(prototypeInstances)).toEqual(before)
  })
})
