import { useTranslation } from 'react-i18next'
import { selectDevices, useLiveStore } from '@/store/live-store'
import type { Device } from '@/types/generated'
import { DeviceDetail } from './DeviceDetail'
import { HubDetail } from './HubDetail'

interface DeviceDrawerProps {
  // 要查看的设备 id（'hub' 为中枢本机）；null 表示关闭
  id: string | null
  hubVersion: string | null
  onClose(): void
  // 吊销与重新配对的入口；没有接入时按钮禁用
  onRevoke?(device: Device): void
  onRepair?(device: Device): void
}

// 设备详情抽屉：URL 的 ?open=<id> 驱动。设备不存在时不渲染（页面负责提示并清除参数）。
export function DeviceDrawer({ id, hubVersion, onClose, onRevoke, onRepair }: DeviceDrawerProps) {
  const devices = useLiveStore(selectDevices)
  useTranslation()
  if (!id) return null
  if (id === 'hub') return <HubDetail onClose={onClose} />
  const device = devices?.find((d) => d.id === id)
  if (!device) return null
  return <DeviceDetail key={id} device={device} hubVersion={hubVersion} onClose={onClose} onRevoke={onRevoke} onRepair={onRepair} />
}
