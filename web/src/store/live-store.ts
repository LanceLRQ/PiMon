import { useSyncExternalStore } from 'react'
import type { Instance, Settings } from '@/types/generated'
import type { Patch, ScreenSettings, Snapshot } from '@/types/protocol.generated'

// 实时数据存储：snapshot 整体覆盖，patch 按实体增量合并。
// 状态对象不可变，每次变更产生新引用，便于 useSyncExternalStore 比较。
export interface LiveState {
  // WebSocket 当前是否连通
  connected: boolean
  // 是否已收到过至少一份 snapshot（用于区分「还没数据」与「数据为空」）
  synced: boolean
  build: string | null
  role: string | null
  settings: Settings | null
  screenSettings: ScreenSettings | null
  instances: Instance[]
  // 服务端时钟减本机时钟（毫秒），用于校正时钟偏差
  clockOffsetMs: number
  // 中枢已升级但本页刷新被防循环机制拦下：外壳提示用户手动刷新
  buildOutdated: boolean
  // 最近一条协议级错误（不影响连接）
  lastError: { code: string; details: Record<string, unknown> } | null
}

export const initialLiveState: LiveState = {
  connected: false,
  synced: false,
  build: null,
  role: null,
  settings: null,
  screenSettings: null,
  instances: [],
  clockOffsetMs: 0,
  buildOutdated: false,
  lastError: null,
}

export interface LiveStore {
  getState(): LiveState
  subscribe(listener: () => void): () => void
  applySnapshot(s: Snapshot): void
  applyPatch(p: Patch): void
  applyServerTime(serverTime: string): void
  setConnected(connected: boolean): void
  setBuildOutdated(outdated: boolean): void
  setError(error: LiveState['lastError']): void
  reset(): void
}

function offsetFrom(serverTime: string): number | null {
  const t = Date.parse(serverTime)
  return Number.isNaN(t) ? null : t - Date.now()
}

export function createLiveStore(): LiveStore {
  let state = initialLiveState
  const listeners = new Set<() => void>()

  function set(next: Partial<LiveState>) {
    state = { ...state, ...next }
    listeners.forEach((l) => l())
  }

  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    // snapshot 随时可能整体覆盖（重连、批量变化时服务端改发），不保留旧实例
    applySnapshot(s) {
      const offset = offsetFrom(s.server_time)
      set({
        synced: true,
        build: s.build,
        role: s.role,
        settings: s.settings ?? null,
        screenSettings: s.screen_settings ?? null,
        instances: s.instances ?? [],
        clockOffsetMs: offset ?? state.clockOffsetMs,
        lastError: null,
      })
    },
    applyPatch(p) {
      const offset = offsetFrom(p.server_time)
      const next: Partial<LiveState> = offset === null ? {} : { clockOffsetMs: offset }
      switch (p.entity) {
        case 'instance_state': {
          const inst = p.instance
          if (!inst) return
          const i = state.instances.findIndex((x) => x.id === inst.id)
          next.instances =
            i < 0 ? [...state.instances, inst] : state.instances.map((x, idx) => (idx === i ? inst : x))
          break
        }
        case 'instance_removed':
          if (!p.id || !state.instances.some((x) => x.id === p.id)) break
          next.instances = state.instances.filter((x) => x.id !== p.id)
          break
        case 'settings':
          if (p.settings) next.settings = p.settings
          if (p.screen_settings) next.screenSettings = p.screen_settings
          break
        default:
          // 未知实体：向前兼容，忽略
          break
      }
      set(next)
    },
    applyServerTime(serverTime) {
      const offset = offsetFrom(serverTime)
      if (offset !== null) set({ clockOffsetMs: offset })
    },
    setConnected: (connected) => set({ connected }),
    setBuildOutdated: (buildOutdated) => set({ buildOutdated }),
    setError: (lastError) => set({ lastError }),
    reset() {
      state = initialLiveState
      listeners.forEach((l) => l())
    },
  }
}

// 应用使用的全局实例；测试用 createLiveStore() 得到独立实例
export const liveStore = createLiveStore()

export function useLiveStore<T>(selector: (s: LiveState) => T, store: LiveStore = liveStore): T {
  return useSyncExternalStore(store.subscribe, () => selector(store.getState()))
}

// 校正后的「现在」：服务端时间，用于判断过期与倒计时
export function serverNow(store: LiveStore = liveStore): number {
  return Date.now() + store.getState().clockOffsetMs
}

export const selectInstances = (s: LiveState) => s.instances
export const selectSettings = (s: LiveState) => s.settings
export const selectBuildOutdated = (s: LiveState) => s.buildOutdated
export const selectConnected = (s: LiveState) => s.connected
