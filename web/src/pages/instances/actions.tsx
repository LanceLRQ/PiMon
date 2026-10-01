import { useCallback, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import type { Instance, InstanceDetail, InstanceRunResult, PluginInfo, ScreenRef } from '@/types/generated'
import { Button } from '@/ui/button'
import { Dialog, DialogContent } from '@/ui/dialog'
import { useToast } from '@/ui/toast'

// 实例操作：立即采集、暂停/恢复、复制、删除（含批量）。结果用轻提示反馈，
// 状态变化靠服务端推送的 patch 更新到各处，这里不改本地列表。

// 手动采集同步执行，最坏约两倍插件超时；客户端超时 = max(60s, 2×插件 timeout + 10s)
export function runTimeoutMs(plugin: PluginInfo | undefined): number {
  const timeout = plugin?.timeout_seconds ?? 0
  return Math.max(60_000, (2 * timeout + 10) * 1000)
}

export interface InstanceActions {
  // 正在执行手动采集的实例 id
  running: ReadonlySet<string>
  run(inst: Instance): Promise<void>
  runMany(list: Instance[]): Promise<void>
  setPaused(inst: Instance, paused: boolean): Promise<void>
  pauseMany(list: Instance[]): Promise<void>
  copy(inst: Instance): Promise<void>
  edit(inst: Instance): void
  requestDelete(list: Instance[]): void
}

interface Manager {
  actions: InstanceActions
  // 删除确认对话框，放在页面里渲染一次即可
  dialogs: ReactNode
}

// 批量请求的并发上限，避免同时触发多个同步采集
const batchConcurrency = 3
// 汇总提示里最多点名的失败实例数，其余折成「另 K 个」
const summaryNames = 3

// 按并发上限依次处理，结果顺序与入参一致
async function pool<T, R>(items: T[], limit: number, fn: (item: T) => Promise<R>): Promise<R[]> {
  const out = new Array<R>(items.length)
  let next = 0
  const worker = async () => {
    while (next < items.length) {
      const i = next++
      out[i] = await fn(items[i])
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker))
  return out
}

interface Outcome {
  inst: Instance
  // 失败时的原始错误；成功为 undefined
  error?: unknown
  failed: boolean
}

export function useInstanceManager(plugins: PluginInfo[] | undefined, onDeleted?: (ids: string[]) => void): Manager {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const navigate = useNavigate()
  const [running, setRunning] = useState<ReadonlySet<string>>(new Set())
  const [deleting, setDeleting] = useState<Instance[] | null>(null)
  const copying = useRef(false)

  const describe = useCallback(
    (err: unknown) => {
      let text = translateErrorValue(i18n, err)
      // 采集错误的详情文字已由中枢脱敏，直接附在译文后
      if (isApiError(err) && (err.code === 'run.failed' || err.code === 'run.timeout') && typeof err.details.message === 'string') {
        text += `：${err.details.message}`
      }
      return text
    },
    [i18n],
  )

  // 单个操作的失败提示带实例名；批量操作改用 summarize 只弹一条
  const failure = useCallback((err: unknown, name: string) => toast.show(`${name}：${describe(err)}`, 'warn'), [describe, toast])

  // 批量结果汇总：全部成功给普通提示，有失败则一条警告列出失败的实例及各自原因
  const summarize = useCallback(
    (outcomes: Outcome[], okMessage: (count: number) => string) => {
      const bad = outcomes.filter((o) => o.failed)
      const ok = outcomes.length - bad.length
      if (bad.length === 0) {
        if (ok > 0) toast.show(okMessage(ok))
        return
      }
      const named = bad.slice(0, summaryNames).map((o) => t('instances.batch.failedItem', { name: o.inst.name, reason: describe(o.error) }))
      const more = bad.length > summaryNames ? t('instances.batch.more', { count: bad.length - summaryNames }) : ''
      toast.show(
        t('instances.batch.summary', { ok, failed: bad.length, list: named.join(t('overview.listSep')) + more }),
        'warn',
      )
    },
    [describe, t, toast],
  )

  const runCore = useCallback(
    async (inst: Instance): Promise<Outcome> => {
      setRunning((s) => new Set(s).add(inst.id))
      try {
        const plugin = plugins?.find((p) => p.id === inst.plugin_id)
        await http.post<InstanceRunResult>(`/api/instances/${inst.id}/run`, undefined, { timeoutMs: runTimeoutMs(plugin) })
        return { inst, failed: false }
      } catch (e) {
        return { inst, failed: true, error: e }
      } finally {
        setRunning((s) => {
          const n = new Set(s)
          n.delete(inst.id)
          return n
        })
      }
    },
    [plugins],
  )

  const pauseCore = useCallback(async (inst: Instance): Promise<Outcome> => {
    try {
      await http.post<Instance>(`/api/instances/${inst.id}/pause`)
      return { inst, failed: false }
    } catch (e) {
      return { inst, failed: true, error: e }
    }
  }, [])

  const actions: InstanceActions = {
    running,
    async run(inst) {
      const o = await runCore(inst)
      if (o.failed) failure(o.error, inst.name)
      else toast.show(t('instances.toast.ran', { name: inst.name }))
    },
    async runMany(list) {
      const outcomes = await pool(list, batchConcurrency, runCore)
      summarize(outcomes, (count) => t('instances.toast.ranMany', { count }))
    },
    async setPaused(inst, paused) {
      try {
        await http.post<Instance>(`/api/instances/${inst.id}/${paused ? 'pause' : 'resume'}`)
        toast.show(t(paused ? 'instances.toast.paused' : 'instances.toast.resumed', { name: inst.name }))
      } catch (e) {
        failure(e, inst.name)
      }
    },
    async pauseMany(list) {
      const outcomes = await pool(
        list.filter((i) => !i.paused),
        batchConcurrency,
        pauseCore,
      )
      summarize(outcomes, (count) => t('instances.toast.pausedMany', { count }))
    },
    async copy(inst) {
      // 复制是创建操作，连点会产生多份
      if (copying.current) return
      copying.current = true
      try {
        const created = await http.post<InstanceDetail>(`/api/instances/${inst.id}/copy`)
        toast.show(t('instances.toast.copied', { name: created.name }))
        navigate(`/instances/${created.id}/edit`)
      } catch (e) {
        failure(e, inst.name)
      } finally {
        copying.current = false
      }
    },
    edit(inst) {
      navigate(`/instances/${inst.id}/edit`)
    },
    requestDelete(list) {
      if (list.length > 0) setDeleting(list)
    },
  }

  const dialogs = (
    <DeleteInstancesDialog
      targets={deleting}
      onClose={() => setDeleting(null)}
      onDone={(ids) => {
        setDeleting(null)
        onDeleted?.(ids)
      }}
    />
  )
  return { actions, dialogs }
}

interface Blocked {
  inst: Instance
  screens: ScreenRef[]
}

function screensOf(err: unknown): ScreenRef[] {
  if (!isApiError(err) || err.code !== 'instance.in_use') return []
  const raw = err.details.screens
  return Array.isArray(raw) ? (raw as ScreenRef[]) : []
}

interface DeleteDialogProps {
  targets: Instance[] | null
  onClose(): void
  // 删除结束（全部完成或用户放弃）后回调，带实际删除的实例 id
  onDone(deleted: string[]): void
}

// 删除确认：先确认；中枢回 409 说明有 screen 引用时，列出受影响的 screen 再二次确认（?confirm=1）
function DeleteInstancesDialog({ targets, onClose, onDone }: DeleteDialogProps) {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const [blocked, setBlocked] = useState<Blocked[]>([])
  const [deleted, setDeleted] = useState<string[]>([])

  const open = targets !== null
  const stage = blocked.length > 0 ? 'affected' : 'confirm'

  function close() {
    const done = deleted
    setBlocked([])
    setDeleted([])
    if (done.length > 0) onDone(done)
    else onClose()
  }

  async function deleteAll(list: Instance[], confirmed: boolean) {
    setBusy(true)
    const gone: string[] = []
    const stillBlocked: Blocked[] = []
    const failed: { inst: Instance; error: unknown }[] = []
    for (const inst of list) {
      try {
        await http.delete(`/api/instances/${inst.id}`, confirmed ? { query: { confirm: 1 } } : undefined)
        gone.push(inst.id)
      } catch (e) {
        const screens = screensOf(e)
        if (screens.length > 0) stillBlocked.push({ inst, screens })
        else failed.push({ inst, error: e })
      }
    }
    setBusy(false)
    const all = [...deleted, ...gone]
    if (failed.length > 0) {
      const named = failed.slice(0, summaryNames).map((f) => t('instances.batch.failedItem', { name: f.inst.name, reason: translateErrorValue(i18n, f.error) }))
      const more = failed.length > summaryNames ? t('instances.batch.more', { count: failed.length - summaryNames }) : ''
      toast.show(t('instances.batch.summary', { ok: gone.length, failed: failed.length, list: named.join(t('overview.listSep')) + more }), 'warn')
    } else if (gone.length > 0) toast.show(t('instances.toast.deleted', { count: gone.length }))
    if (stillBlocked.length > 0) {
      setDeleted(all)
      setBlocked(stillBlocked)
      return
    }
    setBlocked([])
    setDeleted([])
    onDone(all)
  }

  const list = targets ?? []
  const names = list.map((i) => i.name)
  return (
    <Dialog open={open} onOpenChange={(o) => !o && !busy && close()}>
      {open && (
        <DialogContent
          tag="!"
          tagTone="crit"
          title={stage === 'affected' ? t('instances.delete.affectedTitle') : t('instances.delete.title', { count: list.length })}
          footer={
            <>
              <Button variant="outline" className="rounded-[2px]" disabled={busy} onClick={close}>
                {t('common.cancel')}
              </Button>
              <Button
                variant="destructive"
                className="rounded-[2px]"
                disabled={busy}
                onClick={() => void (stage === 'affected' ? deleteAll(blocked.map((b) => b.inst), true) : deleteAll(list, false))}
              >
                {stage === 'affected' ? t('instances.delete.force') : t('instances.delete.confirm')}
              </Button>
            </>
          }
        >
          {stage === 'confirm' ? (
            <div className="space-y-2.5">
              <p>{t('instances.delete.body', { count: list.length })}</p>
              <ul className="max-h-40 list-disc space-y-0.5 overflow-y-auto pl-5 font-mono text-[12px]">
                {names.map((n, i) => (
                  <li key={list[i].id}>{n}</li>
                ))}
              </ul>
              <p className="text-muted-foreground">{t('instances.delete.irreversible')}</p>
            </div>
          ) : (
            <div className="space-y-2.5">
              <p>{t('instances.delete.affectedBody')}</p>
              <ul className="space-y-1.5">
                {blocked.map((b) => (
                  <li key={b.inst.id} className="rounded-[2px] border border-border px-2.5 py-1.5">
                    <div className="font-mono text-[12px]">{b.inst.name}</div>
                    <div className="text-[12px] text-muted-foreground">
                      {t('instances.delete.screens')}
                      {b.screens.map((s) => s.name).join('、')}
                    </div>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </DialogContent>
      )}
    </Dialog>
  )
}
