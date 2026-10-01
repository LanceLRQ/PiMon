import { Link } from 'react-router'
import { useTranslation } from 'react-i18next'
import { Select } from '@/ui/select'
import { useFormContext } from './context'
import type { ControlProps } from './model'

// 本期实例只在 hub 上运行，可选代理限定在 hub 可用或任意位置可用的
const usableOnHub = new Set(['hub', 'any'])

// proxy 字段：下拉「直连」加全局代理列表，空值表示直连
export function ProxyControl({ value, onChange, id, invalid, describedBy }: ControlProps<string>) {
  const { t } = useTranslation()
  const { proxies, proxiesFailed } = useFormContext()
  const usable = (proxies ?? []).filter((p) => usableOnHub.has(p.location))
  const known = value === '' || (proxies ?? []).some((p) => p.id === value)
  const hidden = value !== '' && usable.every((p) => p.id !== value)
  return (
    <div>
      <Select id={id} value={value} invalid={invalid} aria-describedby={describedBy} onChange={(e) => onChange(e.target.value)} className="max-w-[320px]" disabled={proxies === null}>
        <option value="">{t('form.proxyDirect')}</option>
        {usable.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name} · {p.scheme}
          </option>
        ))}
        {hidden && <option value={value}>{known ? t('form.proxyUnavailable', { id: value }) : t('form.proxyMissing', { id: value })}</option>}
      </Select>
      <div className="mt-1 text-[11.5px] text-muted-foreground">
        {proxiesFailed ? t('form.proxyLoadFailed') : t('form.proxyHelp')}{' '}
        <Link to="/proxies" className="underline underline-offset-2">
          {t('form.proxyManage')}
        </Link>
      </div>
    </div>
  )
}
