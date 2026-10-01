import { expect, test } from '@playwright/test'
import { findOverflow, findScreenOverflow } from './support/checks.ts'

// 溢出检查 helper 的自测：故意造出各类溢出，必须都能报出；合法的横向滚动容器不得误报
test.describe('findOverflow 自测', () => {
  test.use({ viewport: { width: 390, height: 800 } })

  test('页面级横向溢出', async ({ page }) => {
    await page.setContent('<div style="width:700px">wide</div>')
    const o = await findOverflow(page)
    expect(o.some((x) => x.kind === 'page')).toBe(true)
  })

  test('带边框容器溢出', async ({ page }) => {
    await page.setContent('<div style="border:1px solid #000;width:200px"><span style="display:inline-block;width:300px">x</span></div>')
    const o = await findOverflow(page)
    expect(o.filter((x) => x.kind === 'container')).toHaveLength(1)
  })

  test('只有背景色的卡片容器溢出', async ({ page }) => {
    await page.setContent('<div style="background:#eee;width:200px"><span style="display:inline-block;width:300px">x</span></div>')
    expect((await findOverflow(page)).filter((x) => x.kind === 'container')).toHaveLength(1)
  })

  test('只有阴影的卡片容器溢出', async ({ page }) => {
    await page.setContent('<div style="box-shadow:0 1px 4px rgba(0,0,0,.4);width:200px"><span style="display:inline-block;width:300px">x</span></div>')
    expect((await findOverflow(page)).filter((x) => x.kind === 'container')).toHaveLength(1)
  })

  test('横向滚动容器与已被裁剪的内容不误报', async ({ page }) => {
    await page.setContent(`
      <div style="border:1px solid #000;width:200px"><div style="overflow-x:auto"><span style="display:inline-block;width:300px">y</span></div></div>
      <div style="border:1px solid #000;width:200px;overflow-x:auto"><span style="display:inline-block;width:300px">z</span></div>`)
    expect(await findOverflow(page)).toEqual([])
  })
})

// 屏幕端溢出检查的自测：造出各类缺陷必须都能报出，合法的截断与贴边内容不得误报
test.describe('findScreenOverflow 自测', () => {
  test.use({ viewport: { width: 400, height: 300 } })
  const widget = (inner: string, style = 'width:200px;height:100px') =>
    `<div data-widget-id="w" style="position:absolute;left:0;top:0;${style}"><div style="width:100%;height:100%">${inner}</div></div>`

  test('干净的小组件不报', async ({ page }) => {
    await page.setContent(widget('<span>ok</span>'))
    expect(await findScreenOverflow(page)).toEqual([])
  })

  test('页面出现滚动', async ({ page }) => {
    await page.setContent('<div style="width:900px;height:900px">big</div>')
    expect((await findScreenOverflow(page)).some((x) => x.startsWith('页面滚动'))).toBe(true)
  })

  test('内容把小组件撑出内框', async ({ page }) => {
    await page.setContent(widget('<div style="width:500px;height:20px">wide</div>'))
    expect((await findScreenOverflow(page)).length).toBeGreaterThan(0)
    await page.setContent(widget('<div style="height:400px">tall</div>'))
    expect((await findScreenOverflow(page)).length).toBeGreaterThan(0)
  })

  test('被省略截断的单行文字不算溢出，没被裁剪的才算', async ({ page }) => {
    await page.setContent(widget('<div style="overflow:hidden;white-space:nowrap;text-overflow:ellipsis;width:100px">some very long text that gets truncated here</div>'))
    expect(await findScreenOverflow(page)).toEqual([])
    await page.setContent(widget('<span style="display:inline-block;width:400px">x</span>'))
    expect((await findScreenOverflow(page)).length).toBeGreaterThan(0)
  })

  test('仪表读数压到圆环线条上要报，缩进环内缘之内不报', async ({ page }) => {
    // 环占 [20,80]×[20,80]，圆心 (50,50)，内缘半径 22.5；读数水平居中、高 16
    const gauge = (readingWidth: number) =>
      widget(
        '<div class="tpl-gauge__ring" style="position:absolute;left:20px;top:20px;width:60px;height:60px"></div>' +
          `<div class="tpl-gauge__reading" style="position:absolute;left:${50 - readingWidth / 2}px;top:42px;width:${readingWidth}px;height:16px"></div>`,
      )
    await page.setContent(gauge(56))
    expect((await findScreenOverflow(page)).some((x) => x.startsWith('仪表'))).toBe(true)
    await page.setContent(gauge(30))
    expect((await findScreenOverflow(page)).filter((x) => x.startsWith('仪表'))).toEqual([])
  })

  test('数值被 overflow:hidden 硬裁必须报出（C-1），单行省略与行数限制放行', async ({ page }) => {
    await page.setContent(widget('<div style="overflow:hidden;white-space:nowrap;width:60px;font:20px monospace">¥123,456,789.12</div>'))
    expect((await findScreenOverflow(page)).some((x) => x.includes('内容被裁剪'))).toBe(true)
    await page.setContent(widget('<div style="overflow:hidden;white-space:nowrap;text-overflow:ellipsis;width:60px;font:20px monospace">some long name here</div>'))
    expect(await findScreenOverflow(page)).toEqual([])
    await page.setContent(widget('<div style="overflow:hidden;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;width:60px;font:16px monospace">aaaa bbbb cccc dddd eeee ffff gggg</div>'))
    expect(await findScreenOverflow(page)).toEqual([])
  })
})
