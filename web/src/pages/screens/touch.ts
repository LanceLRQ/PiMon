/** 屏幕是否有触摸：手动指定优先，自动时取屏幕上报；未上报返回 null */
export function effectiveTouch(inputMode: string, coarse: boolean | undefined): boolean | null {
  if (inputMode === 'touch') return true
  if (inputMode === 'none') return false
  return coarse ?? null
}
