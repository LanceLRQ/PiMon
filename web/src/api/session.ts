import { http } from './client'

// GET /api/session 的响应（src/internal/hub/api/auth.go）
export interface SessionInfo {
  authenticated: boolean
  // 已登录时的会话类型：admin 或 screen
  kind?: 'admin' | 'screen'
  needs_setup: boolean
}

export function fetchSession(): Promise<SessionInfo> {
  return http.get<SessionInfo>('/api/session')
}

export function logout(): Promise<void> {
  return http.post<void>('/api/logout')
}
