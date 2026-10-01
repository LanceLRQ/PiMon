import type { Instance, Report } from '@/types/generated'

// 保存并测试的结果；可经路由 state 带到编辑页，所以只含可序列化数据
export type TestOutcome =
  | { phase: 'running' }
  | { phase: 'ok'; instance: Instance; report: Report | null; at: number; wallMs: number }
  | { phase: 'failed'; code: string; message: string; saved: boolean; at: number }
