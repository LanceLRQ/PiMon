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
import { creatablePlugins } from './catalog'
import { EditorForm } from './EditorForm'
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
  const plugins = usePlugins()
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

  const all = plugins.list?.plugins
  const creatable = useMemo(() => creatablePlugins(all ?? []), [all])
  const detail = detailState.kind === 'ready' ? detailState.detail : null
  const plugin: PluginInfo | null =
    mode === 'new' ? (creatable.find((p) => p.id === selectedId) ?? null) : (all?.find((p) => p.id === detail?.plugin_id) ?? null)

  const ctx = useMemo<FormContextValue>(
    () => ({
      proxies: proxyList.proxies,
      proxiesFailed: proxyList.failed,
      lookup: plugin ? lookupRequest(plugin.id, lang) : async () => [],
    }),
    [proxyList.proxies, proxyList.failed, plugin, lang],
  )

  const title = mode === 'new' ? t('pages.instanceNew') : t('pages.instanceEdit')
  const builtin = creatable.filter((p) => p.origin === 'builtin').length

  const pickPlugin = (p: PluginInfo) => {
    setSelectedId(p.id)
    setOutcome(null)
    setCatalogOpen(false)
  }

  let body: React.ReactNode
  if (plugins.error) {
    body = <Notice tone="crit">{t('editor.withDetail', { summary: t('editor.config.pluginsFailed'), detail: translateErrorValue(i18n, plugins.error) })}</Notice>
  } else if (!all) {
    body = <Notice>{t('shell.loading')}</Notice>
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
  } else if (mode === 'edit' && detail && !plugin) {
    body = <Notice tone="warn">{t('editor.config.pluginMissing', { id: detail.plugin_id })}</Notice>
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
