import type { ThemeProfile } from '../types'

// 来源 ui-demo 方向 1：深空任务控制，信息密度高、告警表现强
export const missionControl: ThemeProfile = {
  id: 'mission-control',
  name: { zh: '任务控制', en: 'Mission Control' },
  nameKey: 'screenThemes.mission-control',
  tone: 'dark',
  palette: 'full',
  radius: 'sharp',
  density: 'compact',
  effects: ['glow'],
  fonts: { display: 'sans', numeric: 'mono', body: 'sans' },
}
