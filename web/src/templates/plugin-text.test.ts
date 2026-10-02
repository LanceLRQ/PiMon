import { describe, expect, it } from 'vitest'
import { createI18n } from '@/i18n'
import { pluginText } from './plugin-text'

describe('pluginText（Ruling 34）', () => {
  it('先查 plugin.<id>.<key>，查到则翻译', async () => {
    const zh = await createI18n('zh')
    const en = await createI18n('en')
    expect(pluginText(zh, 'hub-self', 'online')).toBe('在线')
    expect(pluginText(en, 'hub-self', 'online')).toBe('Online')
    expect(pluginText(zh, 'weather', 'city_required')).toBe('请选择城市')
  })
  it('查不到原样显示，含空格与冒号的任意文本不报错', async () => {
    const zh = await createI18n('zh')
    expect(pluginText(zh, 'hub-self', 'no_such_key')).toBe('no_such_key')
    expect(pluginText(zh, 'x', 'Server error: 500')).toBe('Server error: 500')
    expect(pluginText(zh, undefined, 'online')).toBe('online')
    expect(pluginText(zh, 'hub-self', '')).toBe('')
  })
})
