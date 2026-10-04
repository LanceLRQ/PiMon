import type { Device } from '@/types/generated'

// 测试用设备工厂：默认是一台已确认、在线、兼容的拨入设备。
export function device(id: string, extra: Partial<Device> = {}): Device {
  return {
    id,
    name: id,
    role: 'server',
    direction: 'dial_in',
    address: '',
    hostname: '',
    proxy_id: '',
    allow_offline: false,
    maintenance: false,
    agent_version: 'v1.0.0',
    os: 'linux',
    arch: 'amd64',
    protocol_version: 1,
    replaces: '',
    confirmed: true,
    incompatible: null,
    presence: 'online',
    clock_skew_ms: null,
    last_seen_at: null,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    ...extra,
  }
}
