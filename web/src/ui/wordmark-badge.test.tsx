import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { abbreviate, WordmarkBadge } from './wordmark-badge'

describe('字标徽章', () => {
  it('缩写规则', () => {
    expect(abbreviate('天气')).toBe('天')
    expect(abbreviate('HTTP Check')).toBe('HC')
    expect(abbreviate('http-check-service')).toBe('HCS')
    expect(abbreviate('GLM')).toBe('GLM')
    expect(abbreviate('Codex')).toBe('C')
    expect(abbreviate('')).toBe('?')
  })

  it('渲染缩写并以名称作为可访问名称', () => {
    render(<WordmarkBadge name="OpenAI Codex" />)
    const badge = screen.getByRole('img', { name: 'OpenAI Codex' })
    expect(badge).toHaveTextContent('OC')
  })

  it('可显式指定缩写与尺寸', () => {
    render(<WordmarkBadge name="Claude" abbr="CC" size={36} />)
    const badge = screen.getByRole('img', { name: 'Claude' })
    expect(badge).toHaveTextContent('CC')
    expect(badge).toHaveStyle({ width: '36px', height: '36px' })
  })
})
