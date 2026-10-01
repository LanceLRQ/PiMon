import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { createMemoryRouter, Link, RouterProvider } from 'react-router'
import { afterEach, describe, expect, it } from 'vitest'
import { createI18n } from '@/i18n'
import { setUnsavedGuardBypass, UnsavedGuard } from './unsaved-guard'

afterEach(() => setUnsavedGuardBypass(false))

async function mount() {
  const i18n = await createI18n('zh')
  const router = createMemoryRouter(
    [
      {
        path: '/a',
        element: (
          <>
            <UnsavedGuard dirty textKey="settings.leave" />
            <Link to="/login">去登录</Link>
          </>
        ),
      },
      { path: '/login', element: <div>登录页</div> },
    ],
    { initialEntries: ['/a'] },
  )
  render(
    <I18nextProvider i18n={i18n}>
      <RouterProvider router={router} />
    </I18nextProvider>,
  )
  return router
}

describe('未保存修改的离开保护与会话失效', () => {
  it('普通跳转被拦截', async () => {
    const router = await mount()
    await userEvent.click(screen.getByRole('link', { name: '去登录' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/a')
  })

  it('会话失效或退出登录（bypass 置位）时直接跳转，不弹对话框', async () => {
    setUnsavedGuardBypass(true)
    const router = await mount()
    await userEvent.click(screen.getByRole('link', { name: '去登录' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
