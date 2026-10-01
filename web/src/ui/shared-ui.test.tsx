import { render, screen, within } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { describe, expect, it } from 'vitest'
import { createI18n } from '@/i18n'
import { NumberTag, SectionLabel } from './numbered-label'
import { PageHeader } from './page-header'
import { PiMonLogo } from './pimon-logo'
import { PlaceholderPage } from './placeholder-page'
import { SpecList } from './spec-list'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './table'

describe('共享 UI 件', () => {
  it('NumberTag：显示编号，highlight 时标记', () => {
    const { rerender } = render(<NumberTag no="01.1" />)
    expect(screen.getByText('01.1')).not.toHaveAttribute('data-highlight')
    rerender(<NumberTag no="p3" highlight />)
    expect(screen.getByText('p3')).toHaveAttribute('data-highlight', 'true')
  })

  it('SectionLabel：编号与说明同时出现', () => {
    render(<SectionLabel no="02.3">连接参数</SectionLabel>)
    expect(screen.getByText('02.3')).toBeInTheDocument()
    expect(screen.getByText('连接参数')).toBeInTheDocument()
  })

  it('SpecList：按行渲染标签与取值，支持强调', () => {
    render(
      <SpecList
        rows={[
          { label: '间隔', value: '60 s' },
          { label: '超时', value: '10 s', highlight: true },
        ]}
      />,
    )
    const terms = screen.getAllByRole('term').map((e) => e.textContent)
    expect(terms).toEqual(['间隔', '超时'])
    expect(screen.getByText('60 s').tagName).toBe('DD')
    expect(screen.getByText('10 s')).toHaveClass('text-signal-text')
    expect(screen.getByText('60 s')).toHaveClass('font-mono')
  })

  it('Table：表头、行与单元格语义正确，弱化行带标记', () => {
    render(
      <Table>
        <TableHeader>
          <tr>
            <TableHead>name</TableHead>
            <TableHead>id</TableHead>
          </tr>
        </TableHeader>
        <TableBody>
          <TableRow>
            <TableCell>网关</TableCell>
            <TableCell mono>i-001</TableCell>
          </TableRow>
          <TableRow dimmed>
            <TableCell>过期项</TableCell>
            <TableCell mono>i-002</TableCell>
          </TableRow>
        </TableBody>
      </Table>,
    )
    expect(screen.getAllByRole('columnheader')).toHaveLength(2)
    const rows = screen.getAllByRole('row')
    expect(rows).toHaveLength(3)
    expect(rows[2]).toHaveAttribute('data-dimmed', 'true')
    expect(within(rows[1]).getByText('i-001')).toHaveClass('font-mono')
  })

  it('PageHeader：编号、标题、说明与操作区', () => {
    render(<PageHeader no="03" title="监控实例" sub="共 4 个" actions={<button>新建</button>} />)
    expect(screen.getByRole('heading', { name: '监控实例' })).toBeInTheDocument()
    expect(screen.getByText('03')).toBeInTheDocument()
    expect(screen.getByText('共 4 个')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '新建' })).toBeInTheDocument()
  })

  it('PageHeader：不传编号时不渲染编号框', () => {
    render(<PageHeader title="登录" />)
    expect(screen.getByRole('heading', { name: '登录' }).previousSibling).toBeNull()
  })

  it('PiMonLogo：带可访问名称，多个实例渐变 id 不冲突', () => {
    const { container } = render(
      <>
        <PiMonLogo />
        <PiMonLogo />
      </>,
    )
    expect(screen.getAllByRole('img', { name: 'PiMon' })).toHaveLength(2)
    const ids = Array.from(container.querySelectorAll('radialGradient')).map((g) => g.id)
    expect(new Set(ids).size).toBe(2)
  })

  it('PlaceholderPage：标题走 i18n，M1d 页面标注 M1d', async () => {
    const i18n = await createI18n('zh')
    const { rerender } = render(
      <I18nextProvider i18n={i18n}>
        <PlaceholderPage no="01" titleKey="pages.overview" />
      </I18nextProvider>,
    )
    expect(screen.getByRole('heading', { name: '总览' })).toBeInTheDocument()
    expect(screen.getByText('页面内容将在后续任务中实现。')).toBeInTheDocument()
    rerender(
      <I18nextProvider i18n={i18n}>
        <PlaceholderPage titleKey="pages.layoutEditor" milestone="M1d" />
      </I18nextProvider>,
    )
    expect(screen.getByText(/M1d/)).toBeInTheDocument()
  })
})
