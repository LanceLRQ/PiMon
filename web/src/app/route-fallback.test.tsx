import { render, screen } from '@testing-library/react'
import { lazy, Suspense } from 'react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from '@/i18n'
import { LazyBoundary, RouteFallback } from './route-fallback'

afterEach(() => vi.restoreAllMocks())

describe('页面懒加载占位与失败兜底', () => {
  it('代码块加载期间显示加载中占位，完成后显示页面', async () => {
    const i18n = await createI18n('zh')
    const Page = lazy(() => Promise.resolve({ default: () => <p>页面内容</p> }))
    render(
      <I18nextProvider i18n={i18n}>
        <LazyBoundary>
          <Suspense fallback={<RouteFallback />}>
            <Page />
          </Suspense>
        </LazyBoundary>
      </I18nextProvider>,
    )
    expect(screen.getByRole('status')).toHaveTextContent('加载中')
    expect(await screen.findByText('页面内容')).toBeInTheDocument()
  })

  it('代码块拉取失败时提示刷新而不是白屏', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const i18n = await createI18n('zh')
    const Page = lazy(() => Promise.reject(new Error('Failed to fetch dynamically imported module')))
    render(
      <I18nextProvider i18n={i18n}>
        <LazyBoundary>
          <Suspense fallback={<RouteFallback />}>
            <Page />
          </Suspense>
        </LazyBoundary>
      </I18nextProvider>,
    )
    expect(await screen.findByRole('alert')).toHaveTextContent('页面资源加载失败')
    expect(screen.getByRole('button', { name: '刷新页面' })).toBeInTheDocument()
  })
})
