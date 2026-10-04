import { useTranslation } from 'react-i18next'
import type { Instance } from '@/types/generated'
import { StatusShape } from '@/ui/status-shape'

// 设备上的实例列表：状态形状 + 名称 + 插件 id
export function InstanceRows({ instances, pendingHint }: { instances: Instance[]; pendingHint: boolean }) {
  const { t } = useTranslation()
  if (instances.length === 0) {
    return <div className="px-4 pb-3 text-[12.5px] text-muted-foreground">{pendingHint ? t('devices.detail.noInstancesPending') : t('devices.detail.noInstances')}</div>
  }
  return (
    <ul className="m-0 list-none px-4 pb-3 text-[12.5px]">
      {instances.map((i) => (
        <li key={i.id} className="flex items-center gap-2 border-b border-dotted border-border py-1 last:border-b-0">
          <StatusShape state={i.display_state} />
          <span className="min-w-0 flex-1 truncate">{i.name}</span>
          <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{i.plugin_id}</span>
        </li>
      ))}
    </ul>
  )
}
