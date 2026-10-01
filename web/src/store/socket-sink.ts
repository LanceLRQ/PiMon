import type { Patch, Snapshot } from '@/types/protocol.generated'

/** LiveSocket 向存储写入所需的最小接口：管理端 LiveStore 与屏幕端 ScreenStore 都满足 */
export interface SocketSink {
  applySnapshot(s: Snapshot): void
  applyPatch(p: Patch): void
  applyServerTime(serverTime: string): void
  setConnected(connected: boolean): void
  setBuildOutdated(outdated: boolean): void
  setError(error: { code: string; details: Record<string, unknown> } | null): void
}
