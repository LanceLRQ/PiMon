import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { deviceName } from '@/pages/devices/model'
import { selectDevices, useLiveStore } from '@/store/live-store'
import type { DeviceNames } from './query'

export interface LocationLabels {
  // 设备 id 到设备名，供查询与排序
  names: DeviceNames
  // 运行位置的显示文字；hub 用 hubLabel，已吊销或不存在的设备显示「id（引用失效）」
  text(runsOn: string, hubLabel: string): string
  // 位置是不是已吊销或不存在的设备（设备列表未加载时无法判断，按否）
  missing(runsOn: string): boolean
}

// 实例的运行位置显示：列表、筛选、详情共用，设备名取自实时 store 的设备列表。
export function useLocationLabels(): LocationLabels {
  const { t } = useTranslation()
  const devices = useLiveStore(selectDevices)
  return useMemo(() => {
    const names: DeviceNames = new Map((devices ?? []).map((d) => [d.id, d.name]))
    return {
      names,
      text(runsOn, hubLabel) {
        const r = deviceName(runsOn, devices, hubLabel)
        return r.missing ? t('instances.locationMissing', { id: r.name }) : r.name
      },
      missing: (runsOn) => deviceName(runsOn, devices, '').missing,
    }
  }, [devices, t])
}
