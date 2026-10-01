import { describe, expect, it } from 'vitest'
import zh from '@/i18n/zh.json'
import en from '@/i18n/en.json'
import { creatablePlugins, hiddenFromCatalog } from './catalog'
import { notifierPlugin, simplePlugin } from './editor.fixtures'

describe('creatablePlugins', () => {
  it('只保留数据源插件', () => {
    expect(creatablePlugins([simplePlugin, notifierPlugin]).map((p) => p.id)).toEqual(['simple'])
  })

  it('core 由种子创建，不出现在新建实例目录里', () => {
    const core = { ...simplePlugin, id: 'core', name: '核心' }
    expect(hiddenFromCatalog.has('core')).toBe(true)
    expect(creatablePlugins([core, simplePlugin]).map((p) => p.id)).toEqual(['simple'])
  })
})

describe('插件文本的 i18n 键', () => {
  const lookup = (tree: unknown, path: string[]) => path.reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], tree)

  it.each([
    ['hub-self', ['online']],
    ['hub-self', ['offline']],
    ['hub-self', ['unknown']],
    ['weather', ['weather', 'city_required']],
  ])('plugin.%s.%j 在中英文里都有文案', (id, key) => {
    for (const tree of [zh, en]) {
      const v = lookup(tree, ['plugin', id, ...key])
      expect(typeof v).toBe('string')
      expect((v as string).length).toBeGreaterThan(0)
    }
  })
})
