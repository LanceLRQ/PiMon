import { useSyncExternalStore } from 'react'
import type { SocketSink } from '@/store/socket-sink'
import type { ResolvedLayout, ScreenInstanceData, ScreenState } from '@/types/generated'
import type { StoredScreenData } from './snapshot-cache'
import type { ScreenSettings } from '@/types/protocol.generated'

// 屏幕端实时数据存储：snapshot 整体覆盖，patch 按实体增量合并，状态不可变。
// 与管理端 live-store 分开：屏幕会话收到的是解析后的布局、屏幕状态与小组件数据，没有实例列表。

export interface ScreenStoreState {
  connected: boolean
  synced: boolean
  build: string | null
  settings: ScreenSettings | null
  layout: ResolvedLayout | null
  screenState: ScreenState | null
  /** 实例 id → 展示状态与最新数据项，喂给模板的 InstanceDataMap */
  data: Record<string, ScreenInstanceData>
  /** 服务端时钟减本机时钟（毫秒） */
  clockOffsetMs: number
  /** 最近一次收到布局或数据的服务端时间（毫秒时间戳），断线角标显示「hh:mm 的数据」用 */
  lastDataAt: number | null
  buildOutdated: boolean
  lastError: { code: string; details: Record<string, unknown> } | null
}

export const initialScreenState: ScreenStoreState = {
  connected: false,
  synced: false,
  build: null,
  settings: null,
  layout: null,
  screenState: null,
  data: {},
  clockOffsetMs: 0,
  lastDataAt: null,
  buildOutdated: false,
  lastError: null,
}

export interface ScreenStore extends SocketSink {
  getState(): ScreenStoreState
  subscribe(listener: () => void): () => void
  reset(): void
  /**
   * 用本地存档还原布局、设置与数据（hub 暂时不可达时先显示上次的内容）。
   * 不改时钟校正、不标记 synced；已经收到过真实 snapshot 就忽略，避免旧数据覆盖新数据。
   */
  restore(stored: StoredScreenData): void
}

function parseTime(serverTime: string): number | null {
  const t = Date.parse(serverTime)
  return Number.isNaN(t) ? null : t
}

function indexData(list: ScreenInstanceData[] | undefined): Record<string, ScreenInstanceData> {
  const out: Record<string, ScreenInstanceData> = {}
  for (const d of list ?? []) out[d.instance_id] = d
  return out
}

export function createScreenStore(): ScreenStore {
  let state = initialScreenState
  const listeners = new Set<() => void>()

  function set(next: Partial<ScreenStoreState>) {
    state = { ...state, ...next }
    listeners.forEach((l) => l())
  }

  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    applySnapshot(s) {
      const t = parseTime(s.server_time)
      set({
        synced: true,
        build: s.build,
        settings: s.screen_settings ?? null,
        layout: s.resolved_layout ?? null,
        screenState: s.screen_state ?? null,
        data: indexData(s.screen_data),
        clockOffsetMs: t === null ? state.clockOffsetMs : t - Date.now(),
        lastDataAt: t ?? state.lastDataAt,
        lastError: null,
      })
    },
    applyPatch(p) {
      const t = parseTime(p.server_time)
      const next: Partial<ScreenStoreState> = t === null ? {} : { clockOffsetMs: t - Date.now() }
      switch (p.entity) {
        case 'layout':
          if (!p.resolved_layout) return
          next.layout = p.resolved_layout
          if (t !== null) next.lastDataAt = t
          break
        case 'screen_state':
          if (!p.screen_state) return
          next.screenState = p.screen_state
          break
        case 'screen_data':
          if (!p.screen_data) return
          next.data = { ...state.data, ...indexData(p.screen_data) }
          if (t !== null) next.lastDataAt = t
          break
        case 'settings':
          if (!p.screen_settings) return
          next.settings = p.screen_settings
          break
        default:
          // 未知实体：向前兼容，忽略
          return
      }
      set(next)
    },
    applyServerTime(serverTime) {
      const t = parseTime(serverTime)
      if (t !== null) set({ clockOffsetMs: t - Date.now() })
    },
    setConnected: (connected) => set({ connected }),
    setBuildOutdated: (buildOutdated) => set({ buildOutdated }),
    setError: (lastError) => set({ lastError }),
    restore(stored) {
      if (state.synced) return
      set({
        settings: stored.settings,
        layout: stored.layout,
        screenState: stored.screenState,
        data: stored.data,
        lastDataAt: stored.lastDataAt,
      })
    },
    reset() {
      state = initialScreenState
      listeners.forEach((l) => l())
    },
  }
}

export const screenStore = createScreenStore()

export function useScreenStore<T>(selector: (s: ScreenStoreState) => T, store: ScreenStore = screenStore): T {
  return useSyncExternalStore(store.subscribe, () => selector(store.getState()))
}

/** 校正后的「现在」：服务端时间 */
export function screenNow(store: ScreenStore = screenStore): number {
  return Date.now() + store.getState().clockOffsetMs
}
