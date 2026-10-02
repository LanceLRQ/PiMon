import { fireEvent } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { BrandMark, abbreviate, brandLogoUrl, resetBrandLogoCache } from './brand'
import { renderIn } from './test-utils'

describe('abbreviate（屏幕端字标缩写）', () => {
  it('中日韩取首字、多词取首字母、全大写短词原样', () => {
    expect(abbreviate('')).toBe('?')
    expect(abbreviate('天气')).toBe('天')
    expect(abbreviate('Net Reach')).toBe('NR')
    expect(abbreviate('hub-self')).toBe('HS')
    expect(abbreviate('CPU')).toBe('CPU')
    expect(abbreviate('weather')).toBe('W')
  })
})

describe('BrandMark', () => {
  it('logo 地址按 plugin_id 转义', () => {
    expect(brandLogoUrl('net-reach')).toBe('/api/logos/net-reach')
    expect(brandLogoUrl('a/b')).toBe('/api/logos/a%2Fb')
  })

  it('先用 <img> 加载 logo', async () => {
    resetBrandLogoCache()
    const { container } = await renderIn(<BrandMark pluginId="demo" name="Demo" />)
    const img = container.querySelector('img')!
    expect(img.getAttribute('src')).toBe('/api/logos/demo')
    expect(container.querySelector('[data-brand-fallback]')).toBeNull()
  })

  it('加载失败回落到字标徽章，且只用屏幕 token，不用管理端色类', async () => {
    resetBrandLogoCache()
    const { container } = await renderIn(<BrandMark pluginId="demo" name="Net Reach" />)
    fireEvent.error(container.querySelector('img')!)
    const badge = container.querySelector('[data-brand-fallback]')!
    expect(badge).not.toBeNull()
    expect(container.querySelector('img')).toBeNull()
    expect(badge.textContent).toBe('NR')
    expect(badge.getAttribute('aria-label')).toBe('Net Reach')
    expect(badge.className).toMatch(/s-muted|s-border/)
    expect(badge.className).not.toMatch(/line-strong|panel-2|ink-2/)
  })

  it('失败结果在本页会话内记住，同一插件再次渲染不再请求 logo', async () => {
    resetBrandLogoCache()
    const first = await renderIn(<BrandMark pluginId="demo" name="Demo" />)
    fireEvent.error(first.container.querySelector('img')!)
    first.unmount()
    const second = await renderIn(<BrandMark pluginId="demo" name="Demo" />)
    expect(second.container.querySelector('img')).toBeNull()
    expect(second.container.querySelector('[data-brand-fallback]')).not.toBeNull()
  })
})
