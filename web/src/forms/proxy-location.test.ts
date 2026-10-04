import { describe, expect, it } from 'vitest'
import { proxyUsableAt } from './proxy-location'

// 与后端 instances/machine.go 的 locationAllowed 及 model.ProxyLocation* 的注释逐条对应
describe('proxyUsableAt', () => {
  it('中枢本机：hub、lan、any 都可用', () => {
    expect(proxyUsableAt('hub', 'hub')).toBe(true)
    expect(proxyUsableAt('lan', 'hub')).toBe(true)
    expect(proxyUsableAt('any', 'hub')).toBe(true)
  })
  it('非 VPS 设备：lan 与 any 可用，hub 不可用', () => {
    for (const role of ['server', 'nas', 'dev']) {
      expect(proxyUsableAt('any', role)).toBe(true)
      expect(proxyUsableAt('lan', role)).toBe(true)
      expect(proxyUsableAt('hub', role)).toBe(false)
    }
  })
  it('VPS 设备：只有 any 可用', () => {
    expect(proxyUsableAt('any', 'vps')).toBe(true)
    expect(proxyUsableAt('lan', 'vps')).toBe(false)
    expect(proxyUsableAt('hub', 'vps')).toBe(false)
  })
  it('未知位置值一律不可用', () => {
    expect(proxyUsableAt('mars', 'hub')).toBe(false)
  })
})
