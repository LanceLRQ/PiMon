import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from '@/i18n'
import { Dialog, DialogContent } from '@/ui/dialog'
import { ToastProvider } from '@/ui/toast'
import { CopyButton, copyText } from './copy-button'

let execMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  execMock = vi.fn(() => true)
  Object.defineProperty(document, 'execCommand', { value: execMock, configurable: true, writable: true })
})
afterEach(() => {
  vi.unstubAllGlobals()
  Reflect.deleteProperty(navigator, 'clipboard')
})

function setClipboard(value: unknown) {
  Object.defineProperty(navigator, 'clipboard', { value, configurable: true })
}

describe('copyText', () => {
  it('优先用 navigator.clipboard', async () => {
    const writeText = vi.fn(async () => {})
    setClipboard({ writeText })
    expect(await copyText('abc')).toBe(true)
    expect(writeText).toHaveBeenCalledWith('abc')
    expect(execMock).not.toHaveBeenCalled()
  })

  it('clipboard 被拒绝时退回 execCommand', async () => {
    setClipboard({ writeText: vi.fn(async () => { throw new Error('denied') }) })
    expect(await copyText('abc')).toBe(true)
    expect(execMock).toHaveBeenCalledWith('copy')
  })

  it('clipboard 不存在（http 非安全上下文）时退回 execCommand，临时节点用完即删', async () => {
    expect(await copyText('abc')).toBe(true)
    expect(execMock).toHaveBeenCalledWith('copy')
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('execCommand 返回 false 或抛错时返回 false', async () => {
    execMock.mockReturnValueOnce(false)
    expect(await copyText('abc')).toBe(false)
    execMock.mockImplementationOnce(() => { throw new Error('x') })
    expect(await copyText('abc')).toBe(false)
  })

  it('在对话框焦点陷阱内：临时 textarea 挂在对话框容器里，复制后焦点回到原按钮', async () => {
    let hostOfTextarea: Element | null = null
    execMock.mockImplementation(() => {
      hostOfTextarea = document.querySelector('textarea')?.closest('[role="dialog"]') ?? null
      return true
    })
    const i18n = await createI18n('zh')
    render(
      <I18nextProvider i18n={i18n}>
        <ToastProvider>
          <Dialog open>
            <DialogContent title="t">
              <CopyButton text="hello" />
            </DialogContent>
          </Dialog>
        </ToastProvider>
      </I18nextProvider>,
    )
    const btn = screen.getByRole('button', { name: '复制' })
    await userEvent.click(btn)
    expect(hostOfTextarea).not.toBeNull()
    expect(btn).toHaveFocus()
  })
})

describe('CopyButton', () => {
  async function mount() {
    const i18n = await createI18n('zh')
    render(
      <I18nextProvider i18n={i18n}>
        <ToastProvider>
          <CopyButton text="x" />
        </ToastProvider>
      </I18nextProvider>,
    )
  }

  it('复制成功提示已复制', async () => {
    await mount()
    await userEvent.click(screen.getByRole('button', { name: '复制' }))
    expect(await screen.findByText('已复制')).toBeInTheDocument()
  })

  it('execCommand 返回 false 时提示手动复制', async () => {
    execMock.mockReturnValue(false)
    await mount()
    await userEvent.click(screen.getByRole('button', { name: '复制' }))
    expect(await screen.findByText('复制失败，请手动选中复制')).toBeInTheDocument()
  })
})
