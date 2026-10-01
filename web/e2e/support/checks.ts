import type { Page } from '@playwright/test'

export interface Offender {
  kind: 'page' | 'container'
  detail: string
}

// 页面级与容器级溢出检查，在浏览器内执行。
// 页面级：documentElement.scrollWidth 不得超过视口宽度（不允许横向滚动）。
// 容器级：遍历所有带边框的容器（卡片、分区、表格外框、输入框等），其后代盒子的右边界
// 不得超出容器右边界（容许 1px 误差）；已被中间的滚动容器（overflow-x 非 visible）裁剪的
// 后代、position: fixed 的浮层、svg 内部元素不算。容器自己是横向滚动容器时整体跳过。
export async function findOverflow(page: Page): Promise<Offender[]> {
  return page.evaluate(() => {
    const tolerance = 1
    const out: { kind: 'page' | 'container'; detail: string }[] = []

    const doc = document.documentElement
    if (doc.scrollWidth > window.innerWidth) {
      out.push({ kind: 'page', detail: `scrollWidth ${doc.scrollWidth} > innerWidth ${window.innerWidth}` })
    }

    const describe = (el: Element) => {
      const cls = typeof el.className === 'string' ? el.className.trim().split(/\s+/).slice(0, 4).join('.') : ''
      const text = (el.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 30)
      return `${el.tagName.toLowerCase()}${cls ? '.' + cls : ''}${text ? ` "${text}"` : ''}`
    }
    const clipsX = (el: Element) => {
      const ox = getComputedStyle(el).overflowX
      return ox === 'auto' || ox === 'scroll' || ox === 'hidden' || ox === 'clip'
    }
    const scrollsX = (el: Element) => {
      const ox = getComputedStyle(el).overflowX
      return ox === 'auto' || ox === 'scroll'
    }
    const hasBorder = (el: Element) => {
      const cs = getComputedStyle(el)
      return [cs.borderTopWidth, cs.borderRightWidth, cs.borderBottomWidth, cs.borderLeftWidth].some((w) => parseFloat(w) > 0)
    }

    const seen = new Set<string>()
    for (const container of document.body.querySelectorAll('*')) {
      if (container.closest('svg') || container.childElementCount === 0 || !hasBorder(container)) continue
      if (scrollsX(container)) continue
      const cs = getComputedStyle(container)
      if (cs.display === 'none' || cs.visibility === 'hidden') continue
      const cr = container.getBoundingClientRect()
      if (cr.width === 0 || cr.height === 0) continue
      const limit = cr.right - parseFloat(cs.borderRightWidth) + tolerance
      for (const d of container.querySelectorAll('*')) {
        if (d.closest('svg') && d.tagName.toLowerCase() !== 'svg') continue
        const dcs = getComputedStyle(d)
        if (dcs.display === 'none' || dcs.visibility === 'hidden' || dcs.position === 'fixed') continue
        const dr = d.getBoundingClientRect()
        if (dr.width === 0 || dr.height === 0 || dr.right <= limit) continue
        // 中间层（不含容器本身与后代自身）有横向裁剪时，溢出部分不可见或可滚动，不算缺陷
        let clipped = false
        for (let p = d.parentElement; p && p !== container; p = p.parentElement) {
          if (clipsX(p)) {
            clipped = true
            break
          }
        }
        if (clipped) continue
        const key = `${describe(container)}>${describe(d)}`
        if (seen.has(key)) continue
        seen.add(key)
        out.push({
          kind: 'container',
          detail: `${describe(d)} 右边界 ${dr.right.toFixed(1)} 超出容器 ${describe(container)} 的 ${(limit - tolerance).toFixed(1)}`,
        })
      }
    }
    return out
  })
}

// 收集 console error 与未捕获异常，用于断言零报错
export function collectConsoleErrors(page: Page): string[] {
  const errors: string[] = []
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(`console.error: ${msg.text()} (${msg.location().url})`)
  })
  page.on('pageerror', (err) => errors.push(`pageerror: ${err.message}`))
  return errors
}
