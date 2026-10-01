import type { ThemeProfile } from '../types'

// 来源 ui-demo 方向 4：暖纸白仪器面板 + 信号橙，浅色默认
export const industrial: ThemeProfile = {
  id: 'industrial',
  name: { zh: '工业面板', en: 'Industrial' },
  nameKey: 'screenThemes.industrial',
  tone: 'light',
  palette: 'full',
  radius: 'soft',
  density: 'regular',
  effects: [],
  fonts: { display: 'sans', numeric: 'mono', body: 'sans' },
}
