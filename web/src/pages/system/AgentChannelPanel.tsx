import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { http } from '@/api/client'
import { translateErrorValue } from '@/i18n/errors'
import type { AgentInfo } from '@/types/generated'
import { CopyButton } from '@/ui/copy-button'
import { Note } from '@/ui/note'
import { Section } from '@/ui/section'
import { KV } from './KV'

export function AgentChannelPanel() {
  const { t, i18n } = useTranslation()
  const [info, setInfo] = useState<AgentInfo | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setInfo(await http.get<AgentInfo>('/api/agent/info'))
      setError(null)
    } catch (e) {
      setError(translateErrorValue(i18n, e))
    }
  }, [i18n])
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 进入页面时取一次远端数据
    void load()
  }, [load])

  return (
    <Section no="06.8" title={t('system.agent.title')} meta={t('system.agent.meta')}>
      {error ? (
        <Note tone="crit" role="alert" className="m-3">
          {t('common.withDetail', { summary: t('system.agent.loadFailed'), detail: error })}{' '}
          <button type="button" className="underline underline-offset-2" onClick={() => void load()}>
            {t('common.retry')}
          </button>
        </Note>
      ) : info ? (
        <KV
          rows={[
            { k: t('system.agent.port'), v: info.agent_port > 0 ? String(info.agent_port) : t('system.agent.disabled') },
            {
              k: t('system.agent.fingerprint'),
              v: (
                <span className="inline-flex items-center justify-end gap-2">
                  <code className="break-all">{info.fingerprint}</code>
                  <CopyButton text={info.fingerprint} label={t('system.agent.fingerprintCopy')} />
                </span>
              ),
            },
            { k: t('system.agent.protocol'), v: t('system.agent.protocolValue', { version: info.protocol_version, min: info.min_compatible_version }) },
            { k: t('system.agent.platforms'), v: info.platforms.length > 0 ? info.platforms.join('、') : t('system.agent.noPlatforms') },
          ]}
        />
      ) : (
        <div className="px-4 py-5 text-[12.5px] text-muted-foreground">{t('common.loading')}</div>
      )}
    </Section>
  )
}
