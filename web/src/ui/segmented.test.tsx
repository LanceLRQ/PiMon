import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Segmented } from './segmented'

const options = [
  { value: 'a', label: 'A', title: '选项甲' },
  { value: 'b', label: 'B', title: '选项乙' },
]

describe('Segmented', () => {
  it('以单选组暴露，当前值为选中', () => {
    render(<Segmented ariaLabel="分组" options={options} value="a" onChange={() => {}} />)
    expect(screen.getByRole('radiogroup', { name: '分组' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: '选项甲' })).toBeChecked()
    expect(screen.getByRole('radio', { name: '选项乙' })).not.toBeChecked()
  })

  it('点击触发 onChange', async () => {
    const onChange = vi.fn()
    render(<Segmented ariaLabel="分组" options={options} value="a" onChange={onChange} />)
    await userEvent.click(screen.getByRole('radio', { name: '选项乙' }))
    expect(onChange).toHaveBeenCalledWith('b')
  })
})
