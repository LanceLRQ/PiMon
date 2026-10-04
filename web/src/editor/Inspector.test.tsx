import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { renderWithApp } from '@/pages/instances/test-utils'
import type { LayoutWidget } from '@/types/generated'
import { Inspector, type InspectorProps } from './Inspector'

const hostsWidget: LayoutWidget = {
  id: 'h', source: 'aggregate', template: 'status-grid', size: { cols: 2, rows: 2 }, col: 0, row: 0,
  binding: { data_source: 'hosts' }, options: {},
}
const plainWidget: LayoutWidget = { ...hostsWidget, id: 'p', binding: { refs: [] } }

const props = (widget: LayoutWidget, over: Partial<InspectorProps> = {}): InspectorProps => ({
  widget, screen: undefined, grid: { cols: 8, rows: 5 }, plugins: [], instances: [], template: 'status-grid',
  allowedSizes: [{ cols: 2, rows: 2 }], onMove: vi.fn(), onResize: vi.fn(), onOptions: vi.fn(), onBinding: vi.fn(), onRemove: vi.fn(), ...over,
})

describe('检查器：主机灯组', () => {
  it('不显示数据项选择器，改为说明自动包含中枢与全部设备', async () => {
    await renderWithApp(<Inspector {...props(hostsWidget)} />)
    expect(screen.queryByText('添加数据项')).toBeNull()
    expect(screen.queryByTestId('insp-ref-instance')).toBeNull()
    expect(screen.getByText(/自动包含中枢与全部已确认的设备/)).toBeTruthy()
    expect(screen.getByText('主机灯组')).toBeTruthy()
  })

  it('暴露阈值选项（作用于各机器的 CPU 读数）', async () => {
    const onOptions = vi.fn()
    await renderWithApp(<Inspector {...props(hostsWidget, { onOptions })} />)
    await userEvent.click(screen.getByRole('switch', { name: '手动阈值配色' }))
    expect(onOptions).toHaveBeenCalled()
    expect(onOptions.mock.calls[0][0].threshold).toBeTruthy()
  })

  it('逐项聚合的 status-grid 仍有数据项选择器且没有阈值', async () => {
    await renderWithApp(<Inspector {...props(plainWidget)} />)
    expect(screen.getByText('添加数据项')).toBeTruthy()
    expect(screen.queryByRole('switch', { name: '手动阈值配色' })).toBeNull()
  })
})
