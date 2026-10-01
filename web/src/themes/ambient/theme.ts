import type { ThemeProfile } from '../types'

// 来源 ui-demo 方向 3：暖炭深底 + 柔光琥珀，默认主题
export const ambient: ThemeProfile = {
  id: 'ambient',
  name: { zh: '氛围', en: 'Ambient' },
  nameKey: 'screenThemes.ambient',
  tone: 'dark',
  palette: 'full',
  radius: 'round',
  density: 'airy',
  effects: ['glow'],
  fonts: { display: 'sans', numeric: 'sans', body: 'sans' },
}
