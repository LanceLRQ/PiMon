import { ArrowLeft, History, TriangleAlert } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import { formatDateTime, parseTime } from '@/lib/time'
import type { Layout, LayoutProblem, LayoutState, LayoutVersionInfo } from '@/types/generated'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { Note } from '@/ui/note'
import { Sheet, SheetContent } from '@/ui/sheet'
import { ChangeList } from './ChangeList'
import { diffLayouts } from './changes'
import { LayoutMiniMap } from './LayoutMiniMap'

interface HistorySheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 服务端当前版本与内容（预览时对比用） */
  currentVersion: number
  current: Layout
  /** 草稿里未保存的改动数；回滚会丢弃它们 */
  dirtyCount: number
  /** 回滚成功，拿到新版本 */
  onRolledBack: (state: LayoutState) => void
  onNotify: (message: string, tone?: 'ok' | 'warn') => void
}

function BrokenList({ problems }: { problems: LayoutProblem[] }) {
  const { t } = useTranslation()
  return (
    <ul className="mt-1.5 text-[12.5px]" data-testid="history-broken">
      {problems.map((p, i) => (
        <li key={`${p.screen}/${p.widget}/${i}`} className="flex items-baseline gap-2">
          <span className="font-mono text-[11px] text-muted-foreground">
            {p.screen}/{p.widget}
          </span>
          <span>{t(`layoutEd.broken.${p.code}`, { defaultValue: p.code })}</span>
        </li>
      ))}
    </ul>
  )
}

// 版本历史抽屉：列出最近的版本，可预览、可回滚；回滚会生成新版本，不删除中间的版本。
export function HistorySheet(props: HistorySheetProps) {
  // 每次打开都重新挂载：状态清零，列表重新取（保存、回滚后版本会变）
  return props.open ? <HistoryPanel {...props} /> : null
}

function HistoryPanel({ open, onOpenChange, currentVersion, current, dirtyCount, onRolledBack, onNotify }: HistorySheetProps) {
  const { t, i18n } = useTranslation()
  const [list, setList] = useState<LayoutVersionInfo[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [picked, setPicked] = useState<number | null>(null)
  const [fetched, setFetched] = useState<LayoutState | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const locale = i18n.language === 'en' ? 'en-US' : 'zh-CN'

  useEffect(() => {
    const ctrl = new AbortController()
    http
      .get<LayoutVersionInfo[]>('/api/screens/versions', { signal: ctrl.signal })
      .then(setList)
      .catch((e) => !ctrl.signal.aborted && setError(translateErrorValue(i18n, e)))
    return () => ctrl.abort()
  }, [i18n])

  useEffect(() => {
    if (picked === null) return
    const ctrl = new AbortController()
    http
      .get<LayoutState>(`/api/screens/versions/${picked}`, { signal: ctrl.signal })
      .then(setFetched)
      .catch((e) => !ctrl.signal.aborted && setError(translateErrorValue(i18n, e)))
    return () => ctrl.abort()
  }, [picked, i18n])

  // 只认当前选中版本的响应，切换时旧响应不会闪现
  const detail = fetched?.version === picked ? fetched : null
  // 这个版本相比当前版本会改变什么
  const diff = useMemo(() => (detail ? diffLayouts(current, detail.layout) : []), [detail, current])
  const broken = detail?.broken ?? []

  const rollback = async () => {
    if (picked === null) return
    setBusy(true)
    try {
      const st = await http.post<LayoutState>('/api/screens/rollback', { version: picked })
      setConfirm(false)
      onOpenChange(false)
      onRolledBack(st)
      onNotify(t('layoutEd.history.done', { version: st.version }))
    } catch (e) {
      setConfirm(false)
      setError(translateErrorValue(i18n, e))
    } finally {
      setBusy(false)
    }
  }

  const openList = () => {
    setPicked(null)
    setError(null)
  }
  const pick = (version: number) => {
    setPicked(version)
    setError(null)
  }

  const time = (iso: string) => {
    const ms = parseTime(iso)
    return ms === null ? iso : formatDateTime(ms, locale)
  }

  return (
    <>
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent
          srTitle={t('layoutEd.history.title')}
          header={
            <div className="flex items-center gap-2">
              {picked !== null && (
                <Button variant="ghost" size="icon" aria-label={t('layoutEd.history.back')} className="size-7 rounded-[2px]" onClick={() => openList()}>
                  <ArrowLeft size={15} />
                </Button>
              )}
              <History size={15} />
              <h2 className="text-[14px] font-medium">{picked === null ? t('layoutEd.history.title') : `v${picked}`}</h2>
              {picked === null && list && <span className="text-[12px] text-muted-foreground">{t('layoutEd.history.sub', { n: list.length })}</span>}
            </div>
          }
          footer={
            picked !== null && (
              <Button variant="default" className="rounded-[2px]" disabled={!detail || picked === currentVersion || busy} onClick={() => setConfirm(true)}>
                {t('layoutEd.history.rollback')}
              </Button>
            )
          }
        >
          {error && (
            <Note tone="crit" role="alert" className="m-3.5">
              {error}
            </Note>
          )}
          {picked === null ? (
            <ul data-testid="history-list">
              {list === null && !error && <li className="px-4 py-3 text-[13px] text-muted-foreground">{t('layoutEd.loading')}</li>}
              {list?.length === 0 && <li className="px-4 py-3 text-[13px] text-muted-foreground">{t('layoutEd.history.empty')}</li>}
              {list?.map((v) => (
                <li key={v.version} data-version={v.version} className="flex items-start gap-3 border-b border-border px-4 py-2.5">
                  <span className="w-10 shrink-0 font-mono text-[13px]">v{v.version}</span>
                  <div className="min-w-0 flex-1 text-[12.5px]">
                    <div className="flex flex-wrap items-center gap-x-2">
                      <span>{t(`layoutEd.history.source.${v.source}`, { defaultValue: v.source })}</span>
                      {v.version === currentVersion && <span className="rounded-[2px] border border-foreground px-1.5 text-[11px] leading-[18px]">{t('layoutEd.history.current')}</span>}
                      {v.has_broken && (
                        <span className="inline-flex items-center gap-1 text-status-warn">
                          <TriangleAlert size={12} />
                          {t('layoutEd.history.brokenTag')}
                        </span>
                      )}
                    </div>
                    <div className="text-muted-foreground">{time(v.created_at)}</div>
                    <div className="text-muted-foreground">
                      {t('layoutEd.history.summary', { a: v.summary.widgets_added, r: v.summary.widgets_removed, c: v.summary.widgets_changed })}
                      {v.summary.grid_changed && ` · ${t('layoutEd.history.gridChanged')}`}
                      {v.summary.rolled_back_from ? ` · ${t('layoutEd.history.rolledFrom', { v: v.summary.rolled_back_from })}` : ''}
                    </div>
                  </div>
                  <Button variant="outline" size="sm" className="shrink-0 rounded-[2px]" onClick={() => pick(v.version)}>
                    {t('layoutEd.history.preview')}
                  </Button>
                </li>
              ))}
            </ul>
          ) : (
            <div className="flex flex-col gap-4 p-4">
              {!detail && !error && <p className="text-[13px] text-muted-foreground">{t('layoutEd.loading')}</p>}
              {detail && (
                <>
                  {broken.length > 0 && (
                    <Note tone="warn" icon={<TriangleAlert size={15} />}>
                      <b>{t('layoutEd.history.brokenTitle')}</b>
                      <BrokenList problems={broken} />
                    </Note>
                  )}
                  <LayoutMiniMap layout={detail.layout} />
                  <section>
                    <h3 className="mb-1 text-[12.5px] font-medium">{t('layoutEd.history.diffTitle', { v: currentVersion })}</h3>
                    <div className="border border-border">
                      <ChangeList changes={diff} empty={t('layoutEd.history.noDiff')} testId="history-diff" />
                    </div>
                  </section>
                </>
              )}
            </div>
          )}
        </SheetContent>
      </Sheet>
      <Dialog open={confirm} onOpenChange={(o) => !o && !busy && setConfirm(false)}>
        {confirm && picked !== null && (
          <DialogContent
            tag="!"
            tagTone={broken.length || dirtyCount ? 'crit' : 'default'}
            title={t('layoutEd.history.confirmTitle', { v: picked })}
            footer={
              <>
                <Button variant="outline" className="rounded-[2px]" disabled={busy} onClick={() => setConfirm(false)}>
                  {t('common.cancel')}
                </Button>
                <Button variant="default" className="rounded-[2px]" disabled={busy} onClick={rollback}>
                  {t('layoutEd.history.confirmGo', { v: picked })}
                </Button>
              </>
            }
          >
            <p>{t('layoutEd.history.confirmBody', { v: picked, next: (list?.[0]?.version ?? currentVersion) + 1 })}</p>
            {dirtyCount > 0 && <p className="mt-2 text-status-crit">{t('layoutEd.history.confirmDirty', { n: dirtyCount })}</p>}
            {broken.length > 0 && (
              <div className="mt-2" data-testid="rollback-broken">
                <p>{t('layoutEd.history.confirmBroken', { n: broken.length })}</p>
                <BrokenList problems={broken} />
              </div>
            )}
          </DialogContent>
        )}
      </Dialog>
    </>
  )
}
