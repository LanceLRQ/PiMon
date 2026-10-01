import { describe, expect, it } from 'vitest'
import { defaultPlugins } from './test-utils'
import { runTimeoutMs } from './actions'

const plugin = (timeout: number) => ({ ...defaultPlugins.plugins[0], timeout_seconds: timeout })

describe('手动采集的客户端超时', () => {
  it('取 max(60 秒, 2×插件 timeout + 10 秒)', () => {
    expect(runTimeoutMs(undefined)).toBe(60_000)
    expect(runTimeoutMs(plugin(10))).toBe(60_000)
    expect(runTimeoutMs(plugin(25))).toBe(60_000)
    expect(runTimeoutMs(plugin(30))).toBe(70_000)
    expect(runTimeoutMs(plugin(60))).toBe(130_000)
  })
})
