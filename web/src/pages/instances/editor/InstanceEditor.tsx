import { ChevronDown, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useLocation, useNavigate, useSearchParams } from 'react-router'
import { translateErrorValue } from '@/i18n/errors'
import { FormContext, type FormContextValue } from '@/forms/context'
import { cn } from '@/lib/utils'
import type { PluginInfo } from '@/types/generated'
import { Button } from '@/ui/button'
import { Note } from '@/ui/note'
import { PageHeader } from '@/ui/page-header'
import { usePlugins } from '../use-plugins'
import { selectDevices, useLiveStore } from '@/store/live-store'
import { creatablePlugins } from './catalog'
import { EditorForm } from './EditorForm'
import { defaultDevice, locationRole } from './location'
import { RunsOnSelect } from './RunsOnSelect'
import { Pane } from './Pane'
import { PluginCatalog } from './PluginCatalog'
import { PluginIcon } from './PluginIcon'
import { SavePanel } from './SavePanel'
import type { TestOutcome } from './types'
import { lookupRequest, useInstanceDetail, useProxyList } from './use-editor-data'
import { isApiError } from '@/api/errors'

interface InstanceEditorProps {
  mode: 'new' | 'edit'
  instanceId?: string
}

// 新建与编辑共用的页面：新建是「插件目录 → 配置 → 保存」三栏，编辑是「配置 → 保存」两栏；
// 宽度不足 1100px 时各栏纵向排列。
export function InstanceEditor({ mode, instanceId }: InstanceEditorProps) {
  const { t, i18n } = useTranslation()
  const lang = i18n.language === 'en' ? 'en' : 'zh'
  const devices = useLiveStore(selectDevices)
  // 用户在选择器里选的运行位置；没选时新建为中枢，编辑为实例当前的位置
  const [chosen, setChosen] = useState<string | null>(null)
  const proxyList = useProxyList()
  const location = useLocation()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [outcome, setOutcome] = useState<TestOutcome | null>(null)
  const [autoTest, setAutoTest] = useState(() => (location.state as { autoTest?: boolean } | null)?.autoTest === true)
  const [detailState, setDetail] = useInstanceDetail(mode === 'edit' ? instanceId : undefined)
  const [selectedId, setSelectedId] = useState<string | null>(params.get('plugin'))
  const [catalogOpen, setCatalogOpen] = useState(selectedId === null)
  const [formVersion, setFormVersion] = useState(0)

  const detail = detailState.kind === 'ready' ? detailState.detail : null
  // 编辑时要等详情载入才知道实例在哪台机器上
  const machine: string | null = chosen ?? (mode === 'new' ? 'hub' : (detail?.runs_on ?? null))
  const plugins = usePlugins(machine)

  const all = plugins.list?.plugins
  const creatable = useMemo(() => creatablePlugins(all ?? []), [all])
  const plugin: PluginInfo | null =
    mode === 'new' ? (creatable.find((p) => p.id === selectedId) ?? null) : (all?.find((p) => p.id === detail?.plugin_id) ?? null)

  const ctx = useMemo<FormContextValue>(
    () => ({
      proxies: proxyList.proxies,
      proxiesFailed: proxyList.failed,
      role: locationRole(devices, machine ?? 'hub'),
      lookup: plugin ? lookupRequest(plugin.id, lang) : async () => [],
    }),
    [proxyList.proxies, proxyList.failed, plugin, lang, devices, machine],
  )

  const title = mode === 'new' ? t('pages.instanceNew') : t('pages.instanceEdit')
  const builtin = creatable.filter((p) => p.origin === 'builtin').length

  const pickPlugin = (p: PluginInfo) => {
    // 只能在设备上跑的插件：中枢位置没有意义，改到第一台可用设备（没有则留在中枢并提示）
    if (machine === 'hub' && !p.runs_on.includes('hub')) setChosen(defaultDevice(devices) ?? 'hub')
    setSelectedId(p.id)
    setOutcome(null)
    setCatalogOpen(false)
  }

  // 编辑时位置选择器在表单里；表单显示不出来（取插件失败、位置上没有该插件）时放在提示下面，以便改回
  const recoverPicker = mode === 'edit' && machine ? <RunsOnSelect value={machine} onChange={setChosen} plugin={null} id="f-runs-on" /> : null

  let body: React.ReactNode
  if (plugins.error) {
    body = (
      <Notice tone="crit">
        {t('editor.withDetail', { summary: t('editor.config.pluginsFailed'), detail: translateErrorValue(i18n, plugins.error) })}
        {recoverPicker && <div className="mt-2">{recoverPicker}</div>}
      </Notice>
    )
  } else if (mode === 'edit' && detailState.kind === 'loading') {
    body = <Notice>{t('shell.loading')}</Notice>
  } else if (mode === 'edit' && detailState.kind === 'error') {
    const notFound = isApiError(detailState.error) && detailState.error.code === 'instance.not_found'
    body = (
      <Notice tone="crit">
        {notFound ? t('editor.config.notFound') : t('editor.withDetail', { summary: t('editor.config.loadFailed'), detail: translateErrorValue(i18n, detailState.error) })}{' '}
        <Link to="/instances" className="underline underline-offset-2">
          {t('editor.back')}
        </Link>
      </Notice>
    )
  } else if (!all) {
    body = <Notice>{t('shell.loading')}</Notice>
  } else if (mode === 'edit' && detail && !plugin) {
    body = (
      <Notice tone="warn">
        {machine !== detail.runs_on ? t('editor.config.runsOnNoPlugin', { id: detail.plugin_id }) : t('editor.config.pluginMissing', { id: detail.plugin_id })}
        {recoverPicker && <div className="mt-2">{recoverPicker}</div>}
      </Notice>
    )
  } else if (!plugin) {
    body = (
      <>
        <Pane no="02" title={t('editor.config.title')} className="min-[1100px]:border-r">
          <p className="p-4 text-[13px] text-muted-foreground">{t('editor.config.pick')}</p>
        </Pane>
        <Pane no="03" title={t('editor.save.title')}>
          <SavePanel
            plugin={null}
            name=""
            intervalLabel=""
            errorCount={0}
            busy={null}
            disabled
            runsLabel="—"
            outcome={null}
            onSave={() => {}}
            onSaveTest={() => {}}
          />
        </Pane>
      </>
    )
  } else {
    body = (
      <EditorForm
        key={`${plugin.id}:${formVersion}`}
        mode={mode}
        plugin={plugin}
        detail={detail}
        machine={machine ?? 'hub'}
        onMachine={setChosen}
        outcome={outcome}
        onOutcome={setOutcome}
        autoTest={autoTest}
        onAutoTestStarted={() => {
          setAutoTest(false)
          // 清掉路由 state，刷新后不会再自动运行
          navigate(location.pathname, { replace: true, state: null })
        }}
        onDetail={(d) => {
          setDetail(d)
          setChosen(null)
          setFormVersion((v) => v + 1)
        }}
      />
    )
  }

  return (
    <FormContext.Provider value={ctx}>
      <PageHeader
        no="03"
        title={title}
        sub={t('editor.sub')}
        actions={
          <Button asChild variant="ghost" size="sm" className="rounded-[2px]">
            <Link to="/instances">
              <X /> {t('editor.cancel')}
            </Link>
          </Button>
        }
      />
      <div
        className={cn(
          'flex flex-col min-[1100px]:grid min-[1100px]:min-h-0 min-[1100px]:flex-1',
          mode === 'new' ? 'min-[1100px]:grid-cols-[280px_minmax(0,1fr)_336px]' : 'min-[1100px]:grid-cols-[minmax(0,1fr)_336px]',
        )}
      >
        {mode === 'new' && (
          <Pane
            no="01"
            title={t('editor.catalog.title')}
            meta={t('editor.catalog.meta', { total: creatable.length, builtin, exec: creatable.length - builtin })}
            className="border-b min-[1100px]:border-r min-[1100px]:border-b-0"
          >
            {plugin && (
              <button
                type="button"
                aria-expanded={catalogOpen}
                onClick={() => setCatalogOpen((v) => !v)}
                className="flex items-center gap-2 border-b border-border px-4 py-2.5 text-left text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-ring min-[1100px]:hidden"
              >
                <PluginIcon plugin={plugin} size={15} />
                <span className="min-w-0 flex-1 truncate">{t('editor.catalog.current', { name: plugin.name })}</span>
                <span className="text-muted-foreground">{t('editor.catalog.change')}</span>
                <ChevronDown size={14} aria-hidden className={cn('transition-transform', catalogOpen && 'rotate-180')} />
              </button>
            )}
            {(!plugin || (plugin.runs_on.includes('agent') && machine !== null)) && (
              <div className="border-b border-border px-3 py-2.5">
                <label htmlFor="f-runs-on" className="mb-1 block font-mono text-[11px] text-muted-foreground">
                  {t('editor.config.runsOn')}
                </label>
                <RunsOnSelect id="f-runs-on" value={machine ?? 'hub'} onChange={setChosen} plugin={plugin} />
              </div>
            )}
            <div className={cn('flex min-h-0 flex-1 flex-col', plugin && !catalogOpen && 'max-[1099px]:hidden')}>
              <PluginCatalog plugins={creatable} selectedId={plugin?.id ?? null} onSelect={pickPlugin} />
            </div>
          </Pane>
        )}
        {body}
      </div>
    </FormContext.Provider>
  )
}

function Notice({ children, tone = 'info' }: { children: React.ReactNode; tone?: 'info' | 'warn' | 'crit' }) {
  return (
    <div className="col-span-2 p-4">
      <Note tone={tone} role={tone === 'info' ? 'status' : 'alert'}>
        {children}
      </Note>
    </div>
  )
}
