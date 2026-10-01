import { expect, test } from '@playwright/test'
import { findOverflow } from './support/checks.ts'

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
