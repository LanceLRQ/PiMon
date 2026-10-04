import { render, screen } from '@testing-library/react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'
import { describe, expect, it } from 'vitest'
import { createI18n } from '@/i18n'
import { isDimmedState, resolveShape, shapeGeometry, shapeStates, StatusLabel, StatusShape } from './status-shape'

async function withI18n(node: React.ReactNode, lng: 'zh' | 'en' = 'zh') {
  const i18n = await createI18n(lng)
  return <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
}

describe('状态形状', () => {
  it('13 种形状两两不同（SVG 内容互不相同）', () => {
    expect(shapeStates).toHaveLength(13)
    const markup = shapeStates.map((s) => renderToStaticMarkup(<svg>{shapeGeometry[s]}</svg>))
    expect(new Set(markup).size).toBe(13)
  })

  it('渲染出的 DOM 里每种状态的形状内容也互不相同', async () => {
    const ui = await withI18n(
      <div>
        {shapeStates.map((s) => (
          <StatusShape key={s} state={s} />
        ))}
      </div>,
    )
    const { container } = render(ui)
    const svgs = Array.from(container.querySelectorAll('svg'))
    expect(svgs).toHaveLength(13)
    expect(new Set(svgs.map((s) => s.innerHTML)).size).toBe(13)
  })

  it('带可访问名称，随语言变化', async () => {
    const { unmount } = render(await withI18n(<StatusShape state="critical" />, 'zh'))
    expect(screen.getByRole('img', { name: '严重' })).toBeInTheDocument()
    unmount()
    render(await withI18n(<StatusShape state="critical" />, 'en'))
    expect(screen.getByRole('img', { name: 'Critical' })).toBeInTheDocument()
  })

  it('严重状态加粗描边；未知取值回落为「未知」形状；维护中是独立的扳手形状', async () => {
    const { container } = render(
      await withI18n(
        <>
          <StatusShape state="critical" />
          <StatusShape state="no-such-state" />
          <StatusShape state="maintenance" />
        </>,
      ),
    )
    const [crit, weird, maint] = Array.from(container.querySelectorAll('svg'))
    expect(crit.getAttribute('data-weight')).toBe('strong')
    expect(weird.getAttribute('data-shape')).toBe('unknown')
    expect(maint.getAttribute('data-shape')).toBe('maintenance')
    expect(resolveShape('maintenance')).toBe('maintenance')
  })

  it('连接中断与等待连接是各自独立的专用形状，并按各自名称标注', async () => {
    const { container } = render(await withI18n(<><StatusShape state="disconnected" /><StatusShape state="pending" /></>))
    const [dis, pen] = Array.from(container.querySelectorAll('svg'))
    expect(dis.getAttribute('data-shape')).toBe('disconnected')
    expect(pen.getAttribute('data-shape')).toBe('pending')
    expect(dis.getAttribute('aria-label')).toBe('连接中断')
    expect(pen.getAttribute('aria-label')).toBe('等待连接')
  })

  it('休眠是独立的月牙形状并按「休眠」标注，同样属于弱化态', async () => {
    const { container } = render(await withI18n(<StatusShape state="dormant" />))
    const svg = container.querySelector('svg')!
    expect(svg.getAttribute('data-shape')).toBe('dormant')
    expect(svg.getAttribute('aria-label')).toBe('休眠')
    expect(resolveShape('dormant')).toBe('dormant')
    expect(isDimmedState('dormant')).toBe(true)
  })

  it('过期与离线属于弱化态', () => {
    expect(isDimmedState('stale')).toBe(true)
    expect(isDimmedState('offline')).toBe(true)
    expect(isDimmedState('maintenance')).toBe(true)
    expect(isDimmedState('ok')).toBe(false)
  })

  it('StatusLabel 同时显示形状与文字', async () => {
    render(await withI18n(<StatusLabel state="error" />))
    expect(screen.getByText('采集失败')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: '采集失败' })).toBeInTheDocument()
  })
})
