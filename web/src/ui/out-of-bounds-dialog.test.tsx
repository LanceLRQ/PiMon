import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nextProvider } from 'react-i18next'
import { describe, expect, it, vi } from 'vitest'
import { createI18n } from '@/i18n'
import { OutOfBoundsDialog } from './out-of-bounds-dialog'

const items = [
  { id: 'a', screen: 'index 首页', label: 'CPU', place: 'c7 r5 · 2×1' },
  { id: 'b', screen: 's1 主机', label: '时钟', place: 'c8 r1 · 1×1' },
]

async function mount(open = true) {
  const i18n = await createI18n('zh')
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  render(
    <I18nextProvider i18n={i18n}>
      <OutOfBoundsDialog open={open} grid={{ cols: 6, rows: 4 }} items={items} onConfirm={onConfirm} onCancel={onCancel} />
    </I18nextProvider>,
  )
  return { onConfirm, onCancel }
}

describe('越界处理对话框（共享）', () => {
  it('列出越界的小组件与所在 screen，确认与取消各自回调', async () => {
    const user = userEvent.setup()
    const { onConfirm, onCancel } = await mount()
    expect(screen.getByRole('dialog')).toHaveTextContent('缩小到 6×4 会让 2 个小组件越界')
    const rows = screen.getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(rows[1]).toHaveTextContent('s1 主机')
    expect(rows[1]).toHaveTextContent('时钟')
    await user.click(screen.getByRole('button', { name: '移除 2 个并缩小' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole('button', { name: '保持现有网格' }))
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('Esc 视为取消；关闭状态不渲染', async () => {
    const user = userEvent.setup()
    const { onCancel } = await mount()
    await user.keyboard('{Escape}')
    expect(onCancel).toHaveBeenCalled()
  })

  it('open=false 时不渲染', async () => {
    await mount(false)
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
