import { describe, expect, it } from 'vitest'
import { createI18n } from '@/i18n'
import { device } from './test-utils'
import { deviceName, incompatibleText, needsUpgrade, presenceView } from './model'

describe('deviceName', () => {
  const list = [device('d1', { name: 'NAS' })]
  it('hub 返回传入的中枢名称', () => {
    expect(deviceName('hub', list, '中枢')).toEqual({ name: '中枢', missing: false })
  })
  it('找到设备返回名称', () => {
    expect(deviceName('d1', list, '中枢')).toEqual({ name: 'NAS', missing: false })
  })
  it('设备不存在返回 id 并标记失效；列表未加载（null）时不标记失效', () => {
    expect(deviceName('gone', list, '中枢')).toEqual({ name: 'gone', missing: true })
    expect(deviceName('gone', null, '中枢')).toEqual({ name: 'gone', missing: false })
  })
})

describe('presenceView', () => {
  it('五种在线状态映射到形状、文案键与级别', () => {
    expect(presenceView(device('a', { presence: 'online' }))).toMatchObject({ shape: 'ok', labelKey: 'devices.presence.online', tone: 'ok' })
    expect(presenceView(device('a', { presence: 'disconnected' }))).toMatchObject({ shape: 'stale', tone: 'weak' })
    expect(presenceView(device('a', { presence: 'offline' }))).toMatchObject({ shape: 'offline', tone: 'warn' })
    expect(presenceView(device('a', { presence: 'dormant' }))).toMatchObject({ shape: 'dormant', tone: 'muted' })
    expect(presenceView(device('a', { presence: 'pending' }))).toMatchObject({ shape: 'unconfigured', tone: 'neutral' })
  })
  it('未知取值按「未知」处理', () => {
    expect(presenceView(device('a', { presence: 'weird' }))).toMatchObject({ shape: 'unknown', tone: 'neutral' })
  })
  it('凭据失效或被吊销的拨出设备标记需要重新配对', () => {
    expect(presenceView(device('a', { presence: 'offline', conn_error: 'bad_credential' })).needsRepair).toBe(true)
    expect(presenceView(device('a', { presence: 'offline', conn_error: 'revoked' })).needsRepair).toBe(true)
    expect(presenceView(device('a', { presence: 'offline', conn_error: 'proxy_removed' })).needsRepair).toBe(false)
    expect(presenceView(device('a')).needsRepair).toBe(false)
  })
})

describe('needsUpgrade', () => {
  it('incompatible 非空：版本过旧升级 agent，过新升级中枢', () => {
    expect(needsUpgrade(device('a', { incompatible: { reason: 'version_too_old', required_min: 2 } }), 'v2')).toEqual({ level: 'required', target: 'agent' })
    expect(needsUpgrade(device('a', { incompatible: { reason: 'version_too_new', required_min: 1 } }), 'v2')).toEqual({ level: 'required', target: 'hub' })
  })
  it('无不兼容记录但版本字符串不同且都非空：版本不同', () => {
    expect(needsUpgrade(device('a', { agent_version: 'v1' }), 'v2')).toEqual({ level: 'differs', target: 'agent' })
  })
  it('版本相同、任一为空时不提示', () => {
    expect(needsUpgrade(device('a', { agent_version: 'v2' }), 'v2')).toBeNull()
    expect(needsUpgrade(device('a', { agent_version: '' }), 'v2')).toBeNull()
    expect(needsUpgrade(device('a', { agent_version: 'v1' }), '')).toBeNull()
    expect(needsUpgrade(device('a', { agent_version: 'v1' }), undefined)).toBeNull()
  })
  it('incompatible 优先于版本比较', () => {
    expect(needsUpgrade(device('a', { agent_version: 'v2', incompatible: { reason: 'version_too_old', required_min: 2 } }), 'v2')?.level).toBe('required')
  })
})

describe('incompatibleText', () => {
  it('优先按原因取文案，查不到退回通用文案', async () => {
    const i18n = await createI18n('zh')
    expect(incompatibleText(i18n, 'version_too_old')).toBe('设备上的 agent 版本过旧，请升级 agent')
    expect(incompatibleText(i18n, 'version_too_new')).toBe('设备上的 agent 比中枢新，请升级中枢')
    expect(incompatibleText(i18n, 'something_else')).toBe('设备上的 agent 版本与中枢不兼容，请升级 agent')
    expect(incompatibleText(i18n, undefined)).toBe('设备上的 agent 版本与中枢不兼容，请升级 agent')
  })
  it('英文同样可用', async () => {
    const i18n = await createI18n('en')
    expect(incompatibleText(i18n, 'version_too_new')).toContain('Upgrade the hub')
  })
})
