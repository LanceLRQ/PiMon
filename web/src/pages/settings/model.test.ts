import { describe, expect, it } from 'vitest'
import type { Settings } from '@/types/generated'
import { dirtyKeys, normalizeForSave, trustedProxyErrors } from './model'

const base: Settings = {
  language: 'zh',
  timezone: 'Asia/Shanghai',
  access_url: '',
  trusted_proxies: ['192.168.1.2/32'],
  https_enabled: false,
  reduce_effects: false,
  screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'auto', ui_scale: 1 },
  retention: { raw_hours: 24, five_min_days: 30, hour_days: 365 },
  backup: { daily_at: '04:00', keep: 7 },
}

const clone = (s: Settings): Settings => structuredClone(s)

describe('设置脏状态检测', () => {
  it('完全相同没有脏项', () => {
    expect(dirtyKeys(clone(base), base)).toEqual([])
  })

  it('逐字段识别修改，嵌套字段用点路径', () => {
    const d = clone(base)
    d.language = 'en'
    d.https_enabled = true
    d.retention.raw_hours = 48
    d.backup.keep = 10
    expect(dirtyKeys(d, base)).toEqual(['language', 'https_enabled', 'retention.raw_hours', 'backup.keep'])
  })

  it('反代列表按内容与顺序比较，整个列表算一处', () => {
    const d = clone(base)
    d.trusted_proxies = ['192.168.1.2/32', '10.0.0.0/24']
    expect(dirtyKeys(d, base)).toEqual(['trusted_proxies'])
    d.trusted_proxies = ['192.168.1.2/32']
    expect(dirtyKeys(d, base)).toEqual([])
  })

  it('首尾空白不算修改', () => {
    const d = clone(base)
    d.access_url = '  '
    d.trusted_proxies = [' 192.168.1.2/32 ']
    expect(dirtyKeys(d, base)).toEqual([])
  })

  it('改了又改回原值恢复为干净', () => {
    const d = clone(base)
    d.backup.daily_at = '05:30'
    expect(dirtyKeys(d, base)).toEqual(['backup.daily_at'])
    d.backup.daily_at = '04:00'
    expect(dirtyKeys(d, base)).toEqual([])
  })
})

describe('保存前规整', () => {
  it('去掉访问地址与反代的首尾空白，丢弃空行', () => {
    const d = clone(base)
    d.access_url = ' https://pimon.home.arpa '
    d.trusted_proxies = [' 10.0.0.1 ', '', '  ']
    const out = normalizeForSave(d)
    expect(out.access_url).toBe('https://pimon.home.arpa')
    expect(out.trusted_proxies).toEqual(['10.0.0.1'])
    expect(d.trusted_proxies).toEqual([' 10.0.0.1 ', '', '  '])
  })

  it('屏幕显示参数原样回传且不与草稿共享引用', () => {
    const d = clone(base)
    d.screen.carousel_mode = 'home_only'
    const out = normalizeForSave(d)
    expect(out.screen).toEqual({ ...base.screen, carousel_mode: 'home_only' })
    expect(out.screen).not.toBe(d.screen)
    expect(dirtyKeys(d, base)).toEqual([])
  })
})

describe('服务端字段错误定位', () => {
  it('trusted_proxies[i] 按规整后的下标对应到列表行', () => {
    expect(trustedProxyErrors({ 'trusted_proxies[1]': 'invalid', language: 'invalid', 'trusted_proxies[x]': 'invalid' })).toEqual({ 1: 'invalid' })
  })
})
