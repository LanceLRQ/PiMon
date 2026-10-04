import { useTranslation } from 'react-i18next'
import { deviceName, needsUpgrade, presenceView } from '@/pages/devices/model'
import { useLiveStore, selectDevices } from '@/store/live-store'
import type { PluginInfo } from '@/types/generated'
import { Select } from '@/ui/select'
import { allowedLocations, selectableDevices } from './location'

interface RunsOnSelectProps {
  value: string
  onChange: (machine: string) => void
  // 已选定的插件（限制可选位置）；还没选时为 null
  plugin: PluginInfo | null
  id?: string
}

// 运行位置选择器：中枢 + 各设备（显示在线状态）。插件只能在中枢跑时不显示设备选项。
export function RunsOnSelect({ value, onChange, plugin, id }: RunsOnSelectProps) {
  const { t } = useTranslation()
  const devices = useLiveStore(selectDevices)
  const allowed = allowedLocations(plugin)
  const options = selectableDevices(devices)
  // 已选位置不在列表里（已吊销、未确认或不兼容）时也要显示，免得选择器与实际取值不一致
  const orphan = value !== 'hub' && !options.some((d) => d.id === value)
  const hubLabel = t('editor.config.runsHub')
  const orphanName = deviceName(value, devices, hubLabel).name
  return (
    <Select id={id} aria-label={t('editor.config.runsOn')} value={value} onChange={(e) => onChange(e.target.value)} className="max-w-[320px]">
      <option value="hub" disabled={!allowed.hub && value !== 'hub'}>
        {hubLabel}
      </option>
      {allowed.devices && options.map((d) => (
        <option key={d.id} value={d.id}>
          {d.name} · {t(presenceView(d).labelKey)}
        </option>
      ))}
      {allowed.devices && devices !== null && devices.filter((d) => d.confirmed && needsUpgrade(d, null)?.level === 'required').map((d) => (
        <option key={d.id} value={d.id} disabled>
          {d.name} · {t(`devices.upgrade.${needsUpgrade(d, null)?.target ?? 'agent'}`)}
        </option>
      ))}
      {orphan && !devices?.some((d) => d.id === value && d.confirmed) && <option value={value}>{orphanName}</option>}
    </Select>
  )
}
