import type { Page } from '@playwright/test'

export interface Offender {
  kind: 'page' | 'container'
  detail: string
}

// 页面级与容器级溢出检查，在浏览器内执行。
// 页面级：documentElement.scrollWidth 不得超过视口宽度（不允许横向滚动）。
// 容器级：遍历所有带边框、有实色背景或有阴影的容器（卡片、分区、表格外框、输入框等），其后代盒子的右边界
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
    // 视觉上构成「盒子」的元素：有边框、非透明背景色或阴影
    const isBox = (el: Element) => {
      const cs = getComputedStyle(el)
      const bordered = [cs.borderTopWidth, cs.borderRightWidth, cs.borderBottomWidth, cs.borderLeftWidth].some((w) => parseFloat(w) > 0)
      const bg = cs.backgroundColor
      const filled = bg !== 'rgba(0, 0, 0, 0)' && bg !== 'transparent'
      return bordered || filled || cs.boxShadow !== 'none'
    }

    const seen = new Set<string>()
    for (const container of document.body.querySelectorAll('*')) {
      if (container.closest('svg') || container.childElementCount === 0 || !isBox(container)) continue
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

// 屏幕端（固定网格、禁止滚动）的溢出检查，在浏览器内执行。返回的每一项是一条人类可读的缺陷说明：
//   - 页面：documentElement 与 body 不得出现滚动（scrollWidth/Height 不超过视口）；
//   - 小组件：每个 [data-widget-id] 的内框 scrollHeight ≤ clientHeight、scrollWidth ≤ clientWidth；
//   - 内容：小组件内每个可见后代的包围盒不得越出内框（容许 1px）；被中间的 overflow 裁剪容器截住的后代不算
//     （那是模板有意的截断，如单行省略），也不算 svg 内部元素；
//   - 裁剪：自身 overflow 为 hidden/clip 的元素 scrollWidth/Height 超过 client 即内容被硬裁，仅单行省略与 line-clamp 放行；
//   - 仪表：.tpl-gauge__reading 的四个角必须落在圆环内缘之内（读数与单位不得压到圆环线条或被裁切）。
export async function findScreenOverflow(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const tol = 1
    const out: string[] = []
    const doc = document.documentElement
    if (doc.scrollWidth > window.innerWidth || doc.scrollHeight > window.innerHeight) {
      out.push(`页面滚动：doc ${doc.scrollWidth}x${doc.scrollHeight} > 视口 ${window.innerWidth}x${window.innerHeight}`)
    }
    const body = document.body
    if (body.scrollWidth > window.innerWidth || body.scrollHeight > window.innerHeight) {
      out.push(`页面滚动：body ${body.scrollWidth}x${body.scrollHeight} > 视口 ${window.innerWidth}x${window.innerHeight}`)
    }
    const describe = (el: Element) => {
      const cls = typeof el.className === 'string' ? el.className.trim().split(/\s+/).slice(0, 3).join('.') : ''
      const text = (el.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 24)
      return `${el.tagName.toLowerCase()}${cls ? '.' + cls : ''}${text ? ` "${text}"` : ''}`
    }
    const clips = (el: Element) => {
      const cs = getComputedStyle(el)
      return [cs.overflowX, cs.overflowY].some((o) => o === 'hidden' || o === 'clip' || o === 'auto' || o === 'scroll')
    }
    const seen = new Set<string>()
    for (const holder of document.querySelectorAll<HTMLElement>('[data-widget-id]')) {
      const id = holder.getAttribute('data-widget-id')
      const inner = holder.firstElementChild as HTMLElement | null
      if (!inner) continue
      const box = inner.getBoundingClientRect()
      if (box.width === 0 || box.height === 0) {
        out.push(`小组件 ${id} 没有尺寸`)
        continue
      }
      if (inner.scrollHeight > inner.clientHeight + tol || inner.scrollWidth > inner.clientWidth + tol) {
        out.push(`小组件 ${id} 内容溢出内框：scroll ${inner.scrollWidth}x${inner.scrollHeight} > client ${inner.clientWidth}x${inner.clientHeight}`)
      }
      for (const d of inner.querySelectorAll('*')) {
        if (d.closest('svg') && d.tagName.toLowerCase() !== 'svg') continue
        const cs = getComputedStyle(d)
        if (cs.display === 'none' || cs.visibility === 'hidden') continue
        const r = d.getBoundingClientRect()
        if (r.width === 0 || r.height === 0) continue
        if (r.right <= box.right + tol && r.bottom <= box.bottom + tol && r.left >= box.left - tol && r.top >= box.top - tol) continue
        let clipped = false
        for (let p = d.parentElement; p && p !== holder; p = p.parentElement) {
          if (clips(p)) {
            clipped = true
            break
          }
        }
        if (clipped) continue
        const key = `${id}>${describe(d)}`
        if (seen.has(key)) continue
        seen.add(key)
        out.push(`小组件 ${id} 的 ${describe(d)} 越出内框：[${r.left.toFixed(0)},${r.top.toFixed(0)},${r.right.toFixed(0)},${r.bottom.toFixed(0)}] 外于 [${box.left.toFixed(0)},${box.top.toFixed(0)},${box.right.toFixed(0)},${box.bottom.toFixed(0)}]`)
      }
      // 被裁剪的内容：容器自己 overflow 为 hidden/clip 时，scrollWidth/Height 超过 client 说明有内容被硬裁。
      // 只有「单行省略」（text-overflow:ellipsis 且 white-space:nowrap）与行数限制（line-clamp）是有意截断，其余一律报。
      for (const e of [inner, ...inner.querySelectorAll<HTMLElement>('*')]) {
        if (e.closest('svg')) continue
        const cs = getComputedStyle(e)
        if (cs.display === 'none' || cs.visibility === 'hidden') continue
        const clipsX = cs.overflowX === 'hidden' || cs.overflowX === 'clip'
        const clipsY = cs.overflowY === 'hidden' || cs.overflowY === 'clip'
        if (!clipsX && !clipsY) continue
        const hitX = clipsX && e.scrollWidth > e.clientWidth + tol
        const hitY = clipsY && e.scrollHeight > e.clientHeight + tol
        if (!hitX && !hitY) continue
        const ellipsis = cs.textOverflow === 'ellipsis' && cs.whiteSpace === 'nowrap'
        const clamped = cs.webkitLineClamp !== 'none' && cs.webkitLineClamp !== ''
        if ((hitX && !hitY && ellipsis) || clamped) continue
        const key = `clip:${id}>${describe(e)}`
        if (seen.has(key)) continue
        seen.add(key)
        // 指出越出最多的后代，便于定位是谁撑出去的
        const er = e.getBoundingClientRect()
        let culprit = ''
        let worst = 0
        for (const c of e.querySelectorAll('*')) {
          if (c.closest('svg')) continue
          const r = c.getBoundingClientRect()
          const over = Math.max(r.right - er.right, r.bottom - er.bottom)
          if (over > worst) {
            worst = over
            culprit = ` ← ${describe(c)} 越出 ${over.toFixed(0)}px`
          }
        }
        out.push(`小组件 ${id} 的 ${describe(e)} 内容被裁剪：scroll ${e.scrollWidth}x${e.scrollHeight} > client ${e.clientWidth}x${e.clientHeight}${culprit}`)
      }
      // 仪表：读数（含标记与单位）的四个角都得落在圆环内缘之内（圆环半径 42、线宽 9，内缘半径 37.5，按 100 单位的视窗换算）
      const ring = inner.querySelector('.tpl-gauge__ring')
      const reading = inner.querySelector('.tpl-gauge__reading')
      if (ring && reading) {
        const a = ring.getBoundingClientRect()
        const b = reading.getBoundingClientRect()
        const cx = a.left + a.width / 2
        const cy = a.top + a.height / 2
        const innerR = (a.width * 37.5) / 100
        const far = Math.max(...[b.left, b.right].flatMap((x) => [b.top, b.bottom].map((y) => Math.hypot(x - cx, y - cy))))
        if (far > innerR + tol) {
          out.push(`仪表 ${id} 的读数 [${b.left.toFixed(0)},${b.top.toFixed(0)},${b.right.toFixed(0)},${b.bottom.toFixed(0)}] 压到圆环：角点距圆心 ${far.toFixed(1)} > 内缘半径 ${innerR.toFixed(1)}`)
        }
      }
    }
    return out
  })
}
