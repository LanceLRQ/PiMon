import { render } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { createI18n } from '@/i18n'
import { FIXED_NOW, makeData, makeWidget } from '@/templates/test-utils'
import type { Item, ResolvedLayout, ResolvedScreen, ResolvedWidget, ScreenState } from '@/types/generated'
import type { ScreenSettings, Snapshot } from '@/types/protocol.generated'
import { createScreenStore, type ScreenStore } from './screen-store'
import { ScreenNavigator } from './state-machine'
import { ScreenView, type ScreenReporter } from './ScreenView'

export { FIXED_NOW, makeData, makeWidget }

export const temp: Item = { key: 'temp', type: 'number', value: 21.5, unit: '°C' }
export const humidity: Item = { key: 'humidity', type: 'number', value: 60, unit: '%' }

export function textWidget(id: string, col: number, row: number, text = '你好'): ResolvedWidget {
  return makeWidget({ id, template: 'text', source: 'generic', size: { cols: 2, rows: 1 }, col, row, title: `文本${id}`, options: { text } })
}

export function valueWidget(id: string, col: number, row: number): ResolvedWidget {
  return makeWidget({
    id,
    template: 'value',
    source: 'plugin',
    plugin_id: 'demo',
    size: { cols: 1, rows: 1 },
    col,
    row,
    title: `读数${id}`,
    instance_id: 'i1',
    slots: { value: [{ instance_id: 'i1', item: 'temp', title: '温度' }] },
  })
}

export function screenOf(id: string, widgets: ResolvedWidget[], over: Partial<ResolvedScreen> = {}): ResolvedScreen {
  return { id, name: id, dwell_seconds: 0, in_rotation: true, widgets, ...over }
}

export function layoutOf(screens: ResolvedScreen[], version = 1): ResolvedLayout {
  return { version, grid: { cols: 8, rows: 5 }, screens }
}

export const defaultScreens = (): ResolvedScreen[] => [
  screenOf('index', [textWidget('w1', 0, 0), valueWidget('w2', 2, 0)]),
  screenOf('s1', [textWidget('w3', 0, 0, '第二屏')]),
]

export function settingsOf(over: Partial<ScreenSettings['screen']> = {}, rest: Partial<ScreenSettings> = {}): ScreenSettings {
  return {
    language: 'zh',
    timezone: 'Asia/Shanghai',
    reduce_effects: false,
    screen: { carousel_mode: 'auto', idle_home_seconds: 60, default_dwell_seconds: 15, input_mode: 'none', ui_scale: 1, ...over },
    ...rest,
  }
}

export function snapshotOf(over: Partial<Snapshot> = {}): Snapshot {
  return {
    type: 'snapshot',
    build: 'b1',
    server_time: new Date(Date.now()).toISOString(),
    role: 'screen',
    topics: ['settings', 'layout', 'screen_state', 'screen_data'],
    screen_settings: settingsOf(),
    resolved_layout: layoutOf(defaultScreens()),
    screen_state: { mode: 'on', theme_id: 'ambient', reason: 'schedule' } as ScreenState,
    screen_data: [makeData([temp, humidity])],
    instances: [],
    ...over,
  }
}

export function storeWith(over: Partial<Snapshot> = {}): ScreenStore {
  const s = createScreenStore()
  s.applySnapshot(snapshotOf(over))
  return s
}

export function fakeReporter() {
  const calls: string[] = []
  const reporter: ScreenReporter = {
    reportCurrentScreen: (id) => calls.push(`screen:${id}`),
    onScreenMode: (m) => calls.push(`mode:${m}`),
  }
  return { reporter, calls }
}

export async function renderScreen(store: ScreenStore, opts: { nav?: ScreenNavigator; reporter?: ScreenReporter } = {}) {
  const i18n = await createI18n('zh')
  const nav = opts.nav ?? new ScreenNavigator()
  const view = render(
    <I18nextProvider i18n={i18n}>
      <ScreenView store={store} nav={nav} reporter={opts.reporter} />
    </I18nextProvider>,
  )
  return { ...view, nav }
}

export function setViewport(w: number, h: number) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: w })
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: h })
}
