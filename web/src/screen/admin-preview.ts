import type { SocketSink } from '@/store/socket-sink'
import type { ResolvedLayout, Settings } from '@/types/generated'
import type { Patch, ScreenSettings, Snapshot } from '@/types/protocol.generated'
import type { ScreenStore } from './screen-store'

// 管理员预览 /screen：管理员会话的 WebSocket 收到的是完整设置与原始布局，没有解析后的布局；
// 这里把它适配成屏幕端 store 认识的形状。布局解析结果走 GET /api/screens/resolved，
// 实例数据订阅 screen_data（管理员会话可订阅）。预览端不上报 viewport，也不是屏幕会话，不影响显示器在线状态。

/** 预览订阅的主题：在管理员默认主题之外额外订阅 screen_data；instances 用来感知实例增删与状态变化（解析布局随之变） */
export const previewTopics = ['instances', 'settings', 'layout', 'screen_state', 'screen_data']

/** 完整设置里屏幕端用到的子集 */
export function toScreenSettings(s: Settings): ScreenSettings {
  return { language: s.language, timezone: s.timezone, reduce_effects: s.reduce_effects, screen: s.screen }
}

export class PreviewSink implements SocketSink {
  private resolved: ResolvedLayout
  private seq = 0
  private readonly store: ScreenStore
  private readonly loadResolved: () => Promise<ResolvedLayout>
  private readonly debounceMs: number
  private timer: ReturnType<typeof setTimeout> | null = null
  private pendingTime = ''

  constructor(store: ScreenStore, loadResolved: () => Promise<ResolvedLayout>, initial: ResolvedLayout, debounceMs = 300) {
    this.debounceMs = debounceMs
    this.store = store
    this.loadResolved = loadResolved
    this.resolved = initial
  }

  applySnapshot(s: Snapshot) {
    this.store.applySnapshot({
      ...s,
      screen_settings: s.settings ? toScreenSettings(s.settings) : s.screen_settings,
      resolved_layout: this.resolved,
    })
  }

  applyPatch(p: Patch) {
    switch (p.entity) {
      case 'settings':
        if (p.settings) this.store.applyPatch({ ...p, screen_settings: toScreenSettings(p.settings) })
        return
      case 'layout':
        // 管理员收到的是原始布局：重新取解析后的布局，晚到的旧结果丢弃
        void this.refreshLayout(p.server_time)
        return
      case 'screen_state':
      case 'screen_data':
        this.store.applyPatch(p)
        return
      case 'instance_state':
      case 'instance_removed':
        // 实例增删与展示状态变化会让解析布局变化（真实屏幕会收到新布局）：合并一小段时间内的多次变化再重取
        this.pendingTime = p.server_time
        if (!this.timer) {
          this.timer = setTimeout(() => {
            this.timer = null
            void this.refreshLayout(this.pendingTime)
          }, this.debounceMs)
        }
        return
      default:
        return
    }
  }

  /** 卸载时取消尚未触发的重取 */
  dispose() {
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
    this.seq++
  }

  private async refreshLayout(serverTime: string) {
    const mine = ++this.seq
    try {
      const resolved = await this.loadResolved()
      if (mine !== this.seq) return
      this.resolved = resolved
      this.store.applyPatch({ type: 'patch', server_time: serverTime, entity: 'layout', resolved_layout: resolved })
    } catch {
      // 取不到就保留当前布局，下一次布局变化再试
    }
  }

  applyServerTime(serverTime: string) {
    this.store.applyServerTime(serverTime)
  }
  setConnected(connected: boolean) {
    this.store.setConnected(connected)
  }
  setBuildOutdated(outdated: boolean) {
    this.store.setBuildOutdated(outdated)
  }
  setError(error: { code: string; details: Record<string, unknown> } | null) {
    this.store.setError(error)
  }
}
