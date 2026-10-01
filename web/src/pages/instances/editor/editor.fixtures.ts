import type { InstanceDetail, Instance, PluginInfo, PluginList, Proxy } from '@/types/generated'

// 覆盖全部字段类型的测试插件
export const kitchenPlugin: PluginInfo = {
  id: 'kitchen',
  version: '1.2.0',
  name: '厨房水槽',
  kind: 'source',
  runtime: 'builtin',
  origin: 'builtin',
  runs_on: ['hub'],
  interval_seconds: 60,
  min_interval_seconds: 30,
  timeout_seconds: 20,
  config_schema: [
    { key: 'url', type: 'url', title: '地址', required: true, allow_query: true, help: '要检测的地址。' },
    {
      key: 'method',
      type: 'enum',
      title: '请求方法',
      required: false,
      default: 'GET',
      options: [
        { value: 'GET', title: 'GET' },
        { value: 'HEAD', title: 'HEAD' },
      ],
    },
    { key: 'retries', type: 'number', title: '重试次数', required: false, default: 3, min: 0, max: 10 },
    { key: 'timeout', type: 'duration', title: '超时', required: false, default: '10s', min: 1, max: 25 },
    { key: 'follow', type: 'boolean', title: '跟随重定向', required: false, default: true },
    { key: 'notes', type: 'text', title: '备注', required: false },
    {
      key: 'mode',
      type: 'enum',
      title: '模式',
      required: false,
      default: 'open',
      options: [
        { value: 'open', title: '开放' },
        { value: 'auth', title: '认证' },
      ],
    },
    { key: 'token', type: 'secret', title: '令牌', required: true, visible_when: [{ key: 'mode', values: ['auth'] }] },
    { key: 'hook', type: 'secret_url', title: '回调地址', required: false },
    { key: 'headers', type: 'kv', title: '请求头', required: false, secret_values: true },
    { key: 'hosts', type: 'list', title: '主机列表', required: false, pattern: '^[a-z.]+$' },
    {
      key: 'accounts',
      type: 'object_list',
      title: '账号',
      required: false,
      fields: [
        { key: 'user', type: 'string', title: '用户名', required: true },
        { key: 'password', type: 'secret', title: '密码', required: true },
      ],
    },
    { key: 'city', type: 'lookup', title: '城市', required: false },
    { key: 'proxy', type: 'proxy', title: '代理', required: false },
  ],
  outputs: [
    { key: 'latency', type: 'number', title: '延迟' },
    { key: 'up', type: 'state', title: '可用性' },
  ],
  widgets: [],
}

export const simplePlugin: PluginInfo = {
  id: 'simple',
  version: '1.0.0',
  name: '简单检测',
  kind: 'source',
  runtime: 'exec',
  origin: 'exec',
  runs_on: ['hub'],
  interval_seconds: 300,
  min_interval_seconds: 0,
  timeout_seconds: 10,
  config_schema: [{ key: 'host', type: 'string', title: '主机', required: true }],
  outputs: [],
  widgets: [],
}

export const notifierPlugin: PluginInfo = { ...simplePlugin, id: 'notif', name: '通知渠道', kind: 'notifier', config_schema: [] }

export const editorPlugins: PluginList = {
  plugins: [kitchenPlugin, simplePlugin, notifierPlugin],
  errors: [],
  conflicts: [],
  plugin_dir: '/var/lib/pimon/plugins',
}

export const proxyFixtures: Proxy[] = [
  { id: 'px1', name: 'HK-01', scheme: 'socks5h', address: '1.2.3.4:1080', remote_dns: true, location: 'hub', auth: { set: false } },
  { id: 'px2', name: '仅局域网', scheme: 'http', address: '10.0.0.2:8080', remote_dns: true, location: 'lan', auth: { set: false } },
  { id: 'px3', name: '通用', scheme: 'http', address: '10.0.0.3:8080', remote_dns: true, location: 'any', auth: { set: false } },
] as unknown as Proxy[]

export function makeDetail(over: Partial<InstanceDetail> = {}): InstanceDetail {
  const inst = {
    id: 'i1',
    plugin_id: 'kitchen',
    name: '厨房 A',
    runs_on: 'hub',
    interval_seconds: 0,
    effective_interval_seconds: 60,
    paused: false,
    display_state: 'ok',
    failures: 0,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    ...over,
  } as unknown as Instance
  return {
    ...inst,
    config: { url: 'https://a.example', method: 'GET', mode: 'open', hosts: ['x.y'] },
    report: null,
    ...over,
  } as unknown as InstanceDetail
}
