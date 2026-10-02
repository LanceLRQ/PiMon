import type { ReactElement } from 'react'
import { render } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { createI18n, type Language } from '@/i18n'
import type { Item, ResolvedWidget, ScreenInstanceData } from '@/types/generated'
import { ScreenEnvProvider } from './env'
import { ThemeRoot } from './theme-context'
import type { InstanceDataMap } from './types'

// 固定的「服务器现在」：2026-10-01 06:05:09 UTC（上海 14:05:09）
export const FIXED_NOW = Date.UTC(2026, 9, 1, 6, 5, 9)

export function makeWidget(over: Partial<ResolvedWidget> & { template: string }): ResolvedWidget {
  return {
    id: 'w1',
    source: 'generic',
    size: { cols: 1, rows: 1 },
    col: 0,
    row: 0,
    title: '标题',
    slots: {},
    display_state: 'ok',
    options: {},
    ...over,
  }
}

export function makeData(items: Item[], over: Partial<ScreenInstanceData> = {}): ScreenInstanceData {
  return {
    instance_id: 'i1',
    display_state: 'ok',
    report_status: 'ok',
    report_stale: false,
    summary: '',
    last_success_at: new Date(FIXED_NOW - 60_000).toISOString(),
    items,
    ...over,
  }
}

/** 单实例绑定：value 槽指向实例 i1 的某数据项 */
export function bound(
  template: string,
  size: { cols: number; rows: number },
  items: Item[],
  item = items[0]?.key ?? 'x',
  over: Partial<ResolvedWidget> = {},
  dataOver: Partial<ScreenInstanceData> = {},
): { widget: ResolvedWidget; data: InstanceDataMap } {
  return {
    widget: makeWidget({
      template,
      size,
      instance_id: 'i1',
      slots: { value: [{ instance_id: 'i1', item }] },
      ...over,
    }),
    data: { i1: makeData(items, dataOver) },
  }
}

export interface RenderOpts {
  lang?: Language
  now?: () => number
  timezone?: string
  themeId?: 'ambient' | 'mission-control' | 'industrial'
}

export async function renderIn(ui: ReactElement, opts: RenderOpts = {}) {
  const lang = opts.lang ?? 'zh'
  const i18n = await createI18n(lang)
  return render(
    <I18nextProvider i18n={i18n}>
      <ScreenEnvProvider now={opts.now ?? (() => FIXED_NOW)} timezone={opts.timezone ?? 'Asia/Shanghai'} lang={lang}>
        <ThemeRoot themeId={opts.themeId ?? 'ambient'}>{ui}</ThemeRoot>
      </ScreenEnvProvider>
    </I18nextProvider>,
  )
}
