import { render, screen, within } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { beforeAll, describe, expect, it } from 'vitest'
import { createI18n } from '@/i18n'
import type { Item } from '@/types/generated'
import { ReportItemView } from './report-items'

const now = Date.parse('2026-10-01T12:00:00Z')
let zh: Awaited<ReturnType<typeof createI18n>>
let en: Awaited<ReturnType<typeof createI18n>>

beforeAll(async () => {
  zh = await createI18n('zh')
  en = await createI18n('en')
})

function view(item: Item, title?: string, i18n = zh) {
  return render(
    <I18nextProvider i18n={i18n}>
      <ReportItemView item={item} title={title} now={now} />
    </I18nextProvider>,
  )
}

describe('数据项按类型渲染', () => {
  it('gauge：数值、单位与进度条', () => {
    view({ key: 'cpu', type: 'gauge', value: 37, unit: '%', min: 0, max: 100 }, 'CPU')
    expect(screen.getByText('CPU')).toBeInTheDocument()
    expect(screen.getByText('37')).toBeInTheDocument()
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuenow', '37')
  })

  it('gauge 缺值显示未知而不是 0，也不画进度条', () => {
    view({ key: 'cpu', type: 'gauge', unit: '%', min: 0, max: 100 })
    expect(screen.getByText('未知')).toBeInTheDocument()
    expect(screen.queryByRole('meter')).toBeNull()
    expect(screen.queryByText('0')).toBeNull()
  })

  it('值恰好为 0 如实显示 0', () => {
    view({ key: 'n', type: 'number', value: 0, unit: 'ms' })
    expect(screen.getByText('0')).toBeInTheDocument()
    expect(screen.queryByText('未知')).toBeNull()
  })

  it('number：千分位与单位', () => {
    view({ key: 'n', type: 'number', value: 12345.678, unit: 'MB' })
    expect(screen.getByText('12,345.68')).toBeInTheDocument()
    expect(screen.getByText('MB')).toBeInTheDocument()
  })

  it('quota：剩余百分比、已用总量与重置时间', () => {
    view({
      key: 'q',
      type: 'quota',
      remaining_pct: 72,
      used: 28,
      total: 100,
      unit: '次',
      resets_at: now + 75 * 60 * 1000,
    })
    expect(screen.getByText('剩余 72%')).toBeInTheDocument()
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuenow', '72')
    expect(screen.getByText(/已用 28 次 \/ 总量 100 次/)).toBeInTheDocument()
    expect(screen.getByText(/1 小时 15 分后/)).toBeInTheDocument()
  })

  it('quota 没有任何字段时显示未知', () => {
    view({ key: 'q', type: 'quota' })
    expect(screen.getByText('未知')).toBeInTheDocument()
  })

  it('money：按币种格式化，缺金额为未知', () => {
    const { unmount } = view({ key: 'm', type: 'money', amount: 86.4, currency: 'CNY' })
    expect(screen.getByText(/86\.40/)).toBeInTheDocument()
    unmount()
    view({ key: 'm', type: 'money', currency: 'CNY' })
    expect(screen.getByText('未知')).toBeInTheDocument()
  })

  it('state：形状加文字，未识别的状态按未知', () => {
    const { unmount } = view({ key: 's', type: 'state', state: 'critical', text: '磁盘将满' })
    expect(screen.getByRole('img', { name: '严重' })).toHaveAttribute('data-shape', 'critical')
    expect(screen.getByText('磁盘将满')).toBeInTheDocument()
    unmount()
    view({ key: 's', type: 'state' })
    expect(screen.getByRole('img', { name: '未知' })).toHaveAttribute('data-shape', 'unknown')
  })

  it('text：保留文本；为空时未知', () => {
    const { unmount } = view({ key: 't', type: 'text', text: '多云 18°C' })
    expect(screen.getByText('多云 18°C')).toBeInTheDocument()
    unmount()
    view({ key: 't', type: 'text' })
    expect(screen.getByText('未知')).toBeInTheDocument()
  })

  it('table：列头与行，空单元格显示破折号', () => {
    view({
      key: 'tasks',
      type: 'table',
      columns: ['任务', '状态'],
      rows: [
        ['编译', '运行中'],
        ['测试', null],
      ],
    })
    const table = screen.getByRole('table')
    expect(within(table).getByText('任务')).toBeInTheDocument()
    expect(within(table).getByText('编译')).toBeInTheDocument()
    expect(within(table).getByText('—')).toBeInTheDocument()
  })

  it('条目级错误与过期标记同时显示，数据项仍保留', () => {
    view({ key: 'd', type: 'number', value: 3, stale: true, error: 'mdadm 调用失败' })
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByText('过期')).toBeInTheDocument()
    expect(screen.getByRole('note')).toHaveTextContent('条目错误：mdadm 调用失败')
  })

  it('未知类型给出说明而不是崩溃；英文界面走英文文案', () => {
    view({ key: 'x', type: 'weird' }, undefined, en)
    expect(screen.getByText('Unsupported item type "weird"')).toBeInTheDocument()
  })
})
