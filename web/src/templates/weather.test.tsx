import { describe, expect, it } from 'vitest'
import type { Item } from '@/types/generated'
import { OpenMeteoAttribution } from './weather'
import { WidgetView } from './widget-view'
import { makeData, makeWidget, renderIn } from './test-utils'

const sizes = [
  { cols: 2, rows: 1 },
  { cols: 2, rows: 2 },
  { cols: 4, rows: 2 },
]

const items: Item[] = [
  { key: 'location', type: 'text', text: '上海' },
  { key: 'temperature', type: 'number', value: 23.4, unit: '°C' },
  { key: 'condition', type: 'text', text: '局部多云 / Partly cloudy' },
  { key: 'icon', type: 'text', text: 'partly-cloudy' },
  { key: 'humidity', type: 'gauge', value: 64, unit: '%', min: 0, max: 100 },
  { key: 'wind_speed', type: 'number', value: 12.3, unit: 'km/h' },
  { key: 'wind_direction', type: 'number', value: 270, unit: '°' },
  { key: 'tomorrow_high', type: 'number', value: 27, unit: '°C' },
  { key: 'tomorrow_low', type: 'number', value: 19, unit: '°C' },
  { key: 'tomorrow_icon', type: 'text', text: 'rain' },
  { key: 'attribution', type: 'text', text: 'Weather data by Open-Meteo.com (https://open-meteo.com/), CC BY 4.0' },
]

function weatherWidget(size: { cols: number; rows: number }, over = {}) {
  const slot = (name: string) => [{ instance_id: 'i1', item: name }]
  return makeWidget({
    template: 'weather',
    source: 'plugin',
    plugin_id: 'weather',
    instance_id: 'i1',
    size,
    title: '天气',
    slots: {
      location: slot('location'), temperature: slot('temperature'), condition: slot('condition'), icon: slot('icon'),
      humidity: slot('humidity'), wind_speed: slot('wind_speed'), wind_direction: slot('wind_direction'),
      tomorrow_high: slot('tomorrow_high'), tomorrow_low: slot('tomorrow_low'), tomorrow_icon: slot('tomorrow_icon'),
      attribution: slot('attribution'),
    },
    ...over,
  })
}

describe.each(sizes)('weather 模板 $cols x $rows', (size) => {
  it('显示城市、温度、天气文字与署名；署名用 muted 色', async () => {
    const { container } = await renderIn(<WidgetView widget={weatherWidget(size)} data={{ i1: makeData(items) }} />)
    expect(container.querySelector('[data-template="weather"]')!.getAttribute('data-size')).toBe(`${size.cols}x${size.rows}`)
    expect(container.querySelector('.tpl-weather__location')!.textContent).toBe('上海')
    const temp = container.querySelector('.tpl-weather__temp')!
    expect(temp.textContent).toContain('23.4')
    expect(temp.className).toContain('tabular-nums')
    expect(container.querySelector('.tpl-weather__condition')!.textContent).toBe('局部多云')
    const attr = container.querySelector('.tpl-weather__attribution')!
    expect(attr.textContent).toContain('Open-Meteo')
    expect(attr.className).toContain('text-s-muted-fg')
    expect(container.querySelector('.tpl-weather__icon')).not.toBeNull()
  })

  it('未选城市时显示「请选择城市」占位，不显示 0 度', async () => {
    const setup: Item[] = [{ key: 'setup', type: 'state', state: 'unknown', text: 'city_required' }]
    const { container } = await renderIn(
      <WidgetView widget={weatherWidget(size, { display_state: 'unknown' })} data={{ i1: makeData(setup, { report_status: 'unknown', display_state: 'unknown' }) }} />,
    )
    expect(container.querySelector('.tpl-weather__setup')!.textContent).toContain('请选择城市')
    expect(container.querySelector('.tpl-weather__temp')).toBeNull()
    expect(container.textContent).not.toMatch(/0\s*°/)
  })

  it('温度缺失时显示「未知」', async () => {
    const { container } = await renderIn(<WidgetView widget={weatherWidget(size)} data={{ i1: makeData(items.filter((i) => i.key !== 'temperature')) }} />)
    expect(container.querySelector('.tpl-weather__temp')!.textContent).toContain('未知')
  })
})

describe('weather 模板各尺寸取舍', () => {
  const render = (size: { cols: number; rows: number }, data = items) =>
    renderIn(<WidgetView widget={weatherWidget(size)} data={{ i1: makeData(data) }} />).then((r) => r.container)

  it('2x1 只有当前天气，不含湿度、风与明日', async () => {
    const c = await render({ cols: 2, rows: 1 })
    expect(c.querySelector('.tpl-weather__humidity')).toBeNull()
    expect(c.querySelector('.tpl-weather__wind')).toBeNull()
    expect(c.querySelector('.tpl-weather__tomorrow')).toBeNull()
  })

  it.each([{ cols: 2, rows: 2 }, { cols: 4, rows: 2 }])('$cols x $rows 含湿度、风速风向与明日高低温', async (size) => {
    const c = await render(size)
    expect(c.querySelector('.tpl-weather__humidity')!.textContent).toContain('64')
    expect(c.querySelector('.tpl-weather__wind')!.textContent).toContain('12.3')
    expect(c.querySelector('.tpl-weather__wind [data-wind-degrees="270"]')).not.toBeNull()
    const t = c.querySelector('.tpl-weather__tomorrow')!
    expect(t.textContent).toContain('27')
    expect(t.textContent).toContain('19')
  })

  it('英文界面选英文天气文字', async () => {
    const { container } = await renderIn(<WidgetView widget={weatherWidget({ cols: 2, rows: 1 })} data={{ i1: makeData(items) }} />, { lang: 'en' })
    expect(container.querySelector('.tpl-weather__condition')!.textContent).toBe('Partly cloudy')
  })

  it('超长城市名截断并保留 title', async () => {
    const long = '一个特别特别特别特别特别特别特别长的城市名称'
    const data = items.map((i) => (i.key === 'location' ? { ...i, text: long } : i))
    const c = await render({ cols: 2, rows: 1 }, data)
    const loc = c.querySelector('.tpl-weather__location')!
    expect(loc.className).toContain('truncate')
    expect(loc.getAttribute('title')).toBe(long)
  })

  it('未知天气图标 key 回落到未知图标，不报错', async () => {
    const data = items.map((i) => (i.key === 'icon' ? { ...i, text: 'tornado' } : i))
    const c = await render({ cols: 2, rows: 1 }, data)
    expect(c.querySelector('.tpl-weather__icon[data-weather-icon="unknown"]')).not.toBeNull()
  })
})

describe('OpenMeteoAttribution', () => {
  it('单独渲染时显示署名文字与指向 Open-Meteo 的说明，用 muted 色', async () => {
    const { container } = await renderIn(<OpenMeteoAttribution />)
    expect(container.textContent).toContain('Open-Meteo.com')
    expect(container.querySelector('.tpl-weather__attribution')!.className).toContain('text-s-muted-fg')
  })
})
