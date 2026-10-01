import type { Instance } from '@/types/generated'

// 测试用实例集合：取自管理界面原型的 17 个演示实例（状态按后端 display_state 取值转换）
type Row = [state: string, name: string, plugin: string, runsOn: string, summary: string, ageSeconds: number]

const rows: Row[] = [
  ['critical', 'ubuntu-srv', 'host-metrics', 'agent', '磁盘 95%', 12],
  ['warning', 'Claude', 'ai-claude-quota', 'agent', '周剩余 38%', 60],
  ['warning', '网络连通', 'net-reach', 'hub', '直连 42ms / HK-01 187ms / US-02 超时', 20],
  ['error', '家里 NAS 网页', 'http-check', 'hub', '', 840],
  ['ok', '树莓派本机', 'host-metrics', 'hub', 'CPU 37% · 46.2°C', 5],
  ['ok', '飞牛 NAS', 'host-metrics', 'agent', '已运行 32 天', 8],
  ['ok', 'vps-xray', 'host-metrics', 'agent', '负载 0.42', 9],
  ['ok', 'Codex', 'ai-codex-quota', 'agent', '5h 剩余 72%', 61],
  ['ok', 'GLM', 'ai-glm-quota', 'hub', '5h 剩余 85%', 180],
  ['ok', 'DeepSeek 余额', 'ai-api-balance', 'hub', '¥86.40', 300],
  ['ok', 'OpenRouter 余额', 'ai-api-balance', 'hub', '$12.75', 301],
  ['ok', '天气', 'weather', 'hub', '杭州 多云 18°C', 600],
  ['ok', 'hub 自身', 'hub-self', 'hub', '内存 46 MB · 数据盘剩余 21 GB', 5],
  ['ok', '路由器', 'ping', 'hub', '1.2 ms · 丢包 0%', 10],
  ['ok', 'ubuntu-srv SSH', 'tcp-check', 'hub', '22 端口 · 3 ms', 30],
  ['ok', '个人博客', 'http-check', 'hub', '200 · 312 ms · 证书剩余 64 天', 62],
  ['ok', '系统', 'core', 'hub', '时钟 / 文本', 0],
]

// 固定的「现在」，便于断言相对时间
export const fixtureNow = Date.parse('2026-10-01T12:00:00Z')

export function makeInstance(over: Partial<Instance> & { id: string }): Instance {
  return {
    plugin_id: 'demo',
    name: over.id,
    runs_on: 'hub',
    interval_seconds: 0,
    effective_interval_seconds: 30,
    paused: false,
    display_state: 'ok',
    summary: '',
    report_status: 'ok',
    report_stale: false,
    last_success_at: new Date(fixtureNow - 5000).toISOString(),
    failures: 0,
    created_at: '2026-09-30T00:00:00Z',
    updated_at: '2026-09-30T00:00:00Z',
    ...over,
  }
}

// agent 实例的 runs_on 是主机标识，不是字面量 agent
const agentHosts: Record<string, string> = { 'ubuntu-srv': 'ubuntu-srv', Claude: '开发机', '飞牛 NAS': 'fnos', 'vps-xray': 'vps-xray', Codex: '开发机' }

export const prototypeInstances: Instance[] = rows.map(([state, name, plugin, runsOn, summary, age], i) =>
  makeInstance({
    id: `i${String(i + 1).padStart(2, '0')}`,
    name,
    plugin_id: plugin,
    runs_on: runsOn === 'agent' ? agentHosts[name] : runsOn,
    summary,
    display_state: state,
    report_status: state === 'error' ? '' : state === 'ok' || state === 'warning' || state === 'critical' ? state : 'unknown',
    last_success_at: new Date(fixtureNow - age * 1000).toISOString(),
    last_error: state === 'error' ? '连接被拒绝' : undefined,
    failures: state === 'error' ? 3 : 0,
  }),
)
