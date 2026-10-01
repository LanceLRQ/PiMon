import { useCallback, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router'
import { http } from '@/api/client'
import { isApiError } from '@/api/errors'
import { translateErrorValue } from '@/i18n/errors'
import type { Instance, InstanceRunResult, PluginInfo, ScreenRef } from '@/types/generated'
import type { InstanceDetailView } from './types'
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

export function useInstanceManager(plugins: PluginInfo[] | undefined, onDeleted?: (ids: string[]) => void): Manager {
  const { t, i18n } = useTranslation()
  const toast = useToast()
  const navigate = useNavigate()
  const [running, setRunning] = useState<ReadonlySet<string>>(new Set())
  const [deleting, setDeleting] = useState<Instance[] | null>(null)

  const failure = useCallback(
    (err: unknown) => {
      let text = translateErrorValue(i18n, err)
      // 采集错误的详情文字已由中枢脱敏，直接附在译文后
      if (isApiError(err) && (err.code === 'run.failed' || err.code === 'run.timeout') && typeof err.details.message === 'string') {
        text += `：${err.details.message}`
      }
      toast.show(text, 'warn')
    },
    [i18n, toast],
  )

  const runOne = useCallback(
    async (inst: Instance): Promise<boolean> => {
      setRunning((s) => new Set(s).add(inst.id))
      try {
        const plugin = plugins?.find((p) => p.id === inst.plugin_id)
        await http.post<InstanceRunResult>(`/api/instances/${inst.id}/run`, undefined, { timeoutMs: runTimeoutMs(plugin) })
        return true
      } catch (e) {
        failure(e)
        return false
      } finally {
        setRunning((s) => {
          const n = new Set(s)
          n.delete(inst.id)
          return n
        })
      }
    },
    [plugins, failure],
  )

  const actions: InstanceActions = {
    running,
    async run(inst) {
      if (await runOne(inst)) toast.show(t('instances.toast.ran', { name: inst.name }))
    },
    async runMany(list) {
      const results = await Promise.all(list.map(runOne))
      const ok = results.filter(Boolean).length
      if (ok > 0) toast.show(t('instances.toast.ranMany', { count: ok }))
    },
    async setPaused(inst, paused) {
      try {
        await http.post<Instance>(`/api/instances/${inst.id}/${paused ? 'pause' : 'resume'}`)
        toast.show(t(paused ? 'instances.toast.paused' : 'instances.toast.resumed', { name: inst.name }))
      } catch (e) {
        failure(e)
      }
    },
    async pauseMany(list) {
      const targets = list.filter((i) => !i.paused)
      const results = await Promise.all(
        targets.map(async (inst) => {
          try {
            await http.post<Instance>(`/api/instances/${inst.id}/pause`)
            return true
          } catch (e) {
            failure(e)
            return false
          }
        }),
      )
      const ok = results.filter(Boolean).length
      if (ok > 0) toast.show(t('instances.toast.pausedMany', { count: ok }))
    },
    async copy(inst) {
      try {
        const created = await http.post<InstanceDetailView>(`/api/instances/${inst.id}/copy`)
        toast.show(t('instances.toast.copied', { name: created.name }))
        navigate(`/instances/${created.id}/edit`)
      } catch (e) {
        failure(e)
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
    for (const inst of list) {
      try {
        await http.delete(`/api/instances/${inst.id}`, confirmed ? { query: { confirm: 1 } } : undefined)
        gone.push(inst.id)
      } catch (e) {
        const screens = screensOf(e)
        if (screens.length > 0) stillBlocked.push({ inst, screens })
        else toast.show(`${inst.name}：${translateErrorValue(i18n, e)}`, 'warn')
      }
    }
    setBusy(false)
    const all = [...deleted, ...gone]
    if (gone.length > 0) toast.show(t('instances.toast.deleted', { count: gone.length }))
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
