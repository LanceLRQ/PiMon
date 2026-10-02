/**
 * 屏幕是否有触摸：手动指定优先；自动时 kiosk 的 udev 检测结果（touchscreen）优先，
 * 其次回退到页面上报的 coarse_pointer；两者都没有返回 null（未知）。
 */
export function effectiveTouch(inputMode: string, coarse: boolean | undefined, kioskTouch?: boolean | null): boolean | null {
  if (inputMode === 'touch') return true
  if (inputMode === 'none') return false
  if (kioskTouch != null) return kioskTouch
  return coarse ?? null
}
