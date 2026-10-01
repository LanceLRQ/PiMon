import { render, screen } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { describe, expect, it } from 'vitest'
import { createI18n } from '@/i18n'
import { App } from './App'

describe('App 占位首页', () => {
  it('通过 i18n 显示应用名', async () => {
    const i18n = await createI18n('zh')
    render(
      <I18nextProvider i18n={i18n}>
        <App />
      </I18nextProvider>,
    )
    expect(screen.getByRole('heading', { name: 'PiMon' })).toBeInTheDocument()
  })
})
