import { useRef, type ClipboardEvent, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { normalizeSetupCode, setupCodeGroupLen, splitSetupCode } from '../auth/setup-code'

interface CodeInputProps {
  groups: string[]
  onChange: (groups: string[]) => void
  invalid?: boolean
  disabled?: boolean
}

// 设置码输入：6 组 × 4 位；粘贴整串自动拆分，输满一组自动跳到下一组，空组退格回到上一组
export function CodeInput({ groups, onChange, invalid, disabled }: CodeInputProps) {
  const { t } = useTranslation()
  const refs = useRef<(HTMLInputElement | null)[]>([])

  function focusGroup(i: number) {
    refs.current[i]?.focus()
  }

  function setGroup(i: number, value: string) {
    const next = groups.slice()
    next[i] = value
    onChange(next)
  }

  function onInput(i: number, raw: string) {
    const v = normalizeSetupCode(raw).slice(0, setupCodeGroupLen)
    setGroup(i, v)
    if (v.length === setupCodeGroupLen && i < groups.length - 1) focusGroup(i + 1)
  }

  function onPaste(i: number, e: ClipboardEvent<HTMLInputElement>) {
    const text = normalizeSetupCode(e.clipboardData.getData('text'))
    // 只有一组以内的内容走浏览器默认粘贴
    if (text.length <= setupCodeGroupLen) return
    e.preventDefault()
    const full = text.length >= groups.length * setupCodeGroupLen
    // 整串从第 0 组起拆分；不足整串时从当前组起依次填入
    const next = full ? splitSetupCode(text) : groups.slice()
    let last = groups.length - 1
    if (!full) {
      const chunks = splitSetupCode(text).filter((c) => c !== '')
      chunks.forEach((c, k) => {
        if (i + k < groups.length) next[i + k] = c
      })
      last = Math.min(groups.length - 1, i + chunks.length - 1)
    }
    onChange(next)
    focusGroup(last)
  }

  function onKeyDown(i: number, e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Backspace' && groups[i] === '' && i > 0) {
      e.preventDefault()
      focusGroup(i - 1)
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label={t('setup.code.label')}>
      {groups.map((g, i) => (
        <span key={i} className="flex items-center gap-1.5">
          {i > 0 && <span className="font-mono text-muted-foreground">-</span>}
          <input
            ref={(el) => {
              refs.current[i] = el
            }}
            aria-label={t('setup.code.group', { n: i + 1 })}
            aria-invalid={invalid || undefined}
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
            disabled={disabled}
            maxLength={setupCodeGroupLen}
            value={g}
            onChange={(e) => onInput(i, e.target.value)}
            onPaste={(e) => onPaste(i, e)}
            onKeyDown={(e) => onKeyDown(i, e)}
            className={cn(
              'h-[42px] w-[60px] rounded-[2px] border bg-card text-center font-mono text-lg tracking-[0.08em] uppercase outline-none focus-visible:ring-2 focus-visible:ring-ring mobile:w-[46px]',
              invalid ? 'border-status-crit' : g.length === setupCodeGroupLen ? 'border-foreground' : 'border-line-strong',
            )}
          />
        </span>
      ))}
    </div>
  )
}
