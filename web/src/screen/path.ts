/** 当前路径是否属于屏幕端（/screen 及其子路径）；与 index.html 的内联防闪烁脚本规则一致 */
export function isScreenPath(pathname: string): boolean {
  return pathname === '/screen' || pathname.startsWith('/screen/')
}
