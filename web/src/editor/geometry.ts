// 画布的几何换算：显示器 viewport 缩放进可用区域，指针坐标换回屏幕坐标。

/** 标尺的厚度（CSS 像素，已缩放后的画布外侧） */
export const RULER_SIZE = 22

export const DEFAULT_VIEWPORT = { w: 1024, h: 600 } as const

/** 按显示器比例缩放进可用区域；只缩小不放大，量不到尺寸时按 1 */
export function fitScale(availW: number, availH: number, vw: number, vh: number): number {
  if (availW <= 0 || availH <= 0 || vw <= 0 || vh <= 0) return 1
  return Math.min(1, availW / vw, availH / vh)
}

/** 指针的 client 坐标换成屏幕（未缩放）坐标；rect 是缩放后画布的屏幕位置 */
export function pointerToScreen(clientX: number, clientY: number, rect: { left: number; top: number }, scale: number): { x: number; y: number } {
  const s = scale > 0 ? scale : 1
  return { x: (clientX - rect.left) / s, y: (clientY - rect.top) / s }
}

/** 画布区的内边距（四周各一份） */
export const CANVAS_PAD = 12

/**
 * 画布加标尺整体 contain 缩放进容器：areaW、areaH 是容器的内容尺寸（含内边距），
 * 缩放后 屏幕宽 + 标尺槽 ≤ 容器宽 - 2×内边距，高同理；只缩小不放大。
 */
export function fitCanvasScale(areaW: number, areaH: number, vw: number, vh: number, ruler = RULER_SIZE, pad = CANVAS_PAD): number {
  return fitScale(areaW - 2 * pad - ruler, areaH - 2 * pad - ruler, vw, vh)
}
