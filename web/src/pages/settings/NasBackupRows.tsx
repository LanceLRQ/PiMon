import { useTranslation } from 'react-i18next'
import type { BackupRemote, BackupRemoteInput } from '@/types/generated'
import { Button } from '@/ui/button'
import { Input, PasswordInput } from '@/ui/input'
import { Note } from '@/ui/note'
import { FieldError, FieldHelp, FormRow } from '@/ui/section'
import { Segmented } from '@/ui/segmented'
import { Switch } from '@/ui/switch'

export interface NasTestState {
  testing: boolean
  // 最近一次测试的结果文案；ok 决定语气
  // detail 是后端已脱敏的原因说明，作为分类文案的补充
  result: { ok: boolean; text: string; detail?: string } | null
}

interface Props {
  form: BackupRemoteInput
  // 已保存的配置，用来判断密钥是否已设置；读取失败时为 null
  saved: BackupRemote | null
  // 首次读取或重新读取进行中
  loading: boolean
  loadFailed: boolean
  // 刚保存且口令已更改：在口令字段旁显示持久提示
  passphraseNotice: boolean
  onRetry(): void
  changedKeys: (keyof BackupRemoteInput)[]
  errorText(key: string): string | null
  test: NasTestState
  onChange(patch: Partial<BackupRemoteInput>, clearKeys?: string[]): void
  onTest(): void
}

// 设置页「NAS 备份」的表单行：启用、类型、随类型切换的字段、备份口令、保留份数、测试连接。
// 草稿与保存由 SettingsPage 统一管理，这里只负责渲染与回调。
const smbKeys = ['smb_host', 'smb_port', 'smb_share', 'smb_dir', 'smb_username', 'smb_password', 'smb_domain']
const webdavKeys = ['webdav_url', 'webdav_dir', 'webdav_username', 'webdav_password']

export function NasBackupRows({ form, saved, loading, loadFailed, passphraseNotice, onRetry, changedKeys, errorText, test, onChange, onTest }: Props) {
  const { t } = useTranslation()
  const mark = (...keys: (keyof BackupRemoteInput)[]) => ({
    changed: keys.some((k) => changedKeys.includes(k)),
    changedLabel: t('settings.changed'),
  })
  const isSmb = form.type !== 'webdav'
  const secretPlaceholder = (isSet: boolean) => (isSet ? t('form.secretKeep') : t('form.secretNew'))

  // 文本字段：同时处理错误展示，错误键是服务端的字段名
  const text = (key: Exclude<keyof BackupRemoteInput, 'enabled' | 'smb_port' | 'keep'>, id: string, extra: { placeholder?: string; mono?: boolean } = {}) => (
    <Input
      id={id}
      className={`max-w-[360px] ${extra.mono === false ? '' : 'font-mono'}`}
      autoComplete="off"
      placeholder={extra.placeholder}
      value={form[key] as string}
      invalid={!!errorText(key)}
      onChange={(e) => onChange({ [key]: e.target.value }, [key])}
    />
  )
  const err = (key: string) => errorText(key) && <FieldError>{errorText(key)}</FieldError>

  return (
    <>
      {loading && (
        <div className="border-t border-border px-4 py-3">
          <Note tone="info" role="status">
            {t('settings.backup.remote.loading')}
          </Note>
        </div>
      )}
      {loadFailed && !loading && (
        <div className="border-t border-border px-4 py-3">
          <Note tone="warn" role="status">
            {t('settings.backup.remote.loadFailed')}{' '}
            <button type="button" className="underline underline-offset-2" onClick={onRetry}>
              {t('settings.backup.remote.reload')}
            </button>
          </Note>
        </div>
      )}
      {/* 读取中或读取失败时整组字段与测试按钮禁用（fieldset 向下传递 disabled） */}
      <fieldset disabled={loading || loadFailed} className="m-0 contents border-0 p-0">
      <FormRow label={t('settings.backup.nas')} fieldKey="backup.remote.enabled" {...mark('enabled')}>
        <div className="flex items-center gap-3">
          <Switch checked={form.enabled} onChange={(enabled) => onChange({ enabled })} ariaLabel={t('settings.backup.remote.enabled')} />
          <span className="text-[12.5px] text-ink-2">{t('settings.backup.remote.enabled')}</span>
        </div>
        <FieldHelp>{t('settings.backup.nasHelp')}</FieldHelp>
      </FormRow>
      <FormRow label={t('settings.backup.remote.type')} fieldKey="type" {...mark('type')}>
        <Segmented
          ariaLabel={t('settings.backup.remote.type')}
          value={isSmb ? 'smb' : 'webdav'}
          onChange={(type) => onChange({ type }, ['type', ...(type === 'webdav' ? smbKeys : webdavKeys)])}
          options={[
            { value: 'smb', label: 'SMB', title: 'SMB' },
            { value: 'webdav', label: 'WebDAV', title: 'WebDAV' },
          ]}
        />
        {err('type')}
      </FormRow>

      {isSmb ? (
        <>
          <FormRow label={t('settings.backup.remote.host')} fieldKey="smb_host" htmlFor="nas-smb-host" {...mark('smb_host')}>
            {text('smb_host', 'nas-smb-host')}
            {err('smb_host')}
          </FormRow>
          <FormRow label={t('settings.backup.remote.port')} fieldKey="smb_port" htmlFor="nas-smb-port" {...mark('smb_port')}>
            <Input
              id="nas-smb-port"
              type="number"
              min={1}
              max={65535}
              className="w-[110px] font-mono"
              value={form.smb_port}
              invalid={!!errorText('smb_port')}
              onChange={(e) => onChange({ smb_port: Number(e.target.value) }, ['smb_port'])}
            />
            {err('smb_port')}
          </FormRow>
          <FormRow label={t('settings.backup.remote.share')} fieldKey="smb_share" htmlFor="nas-smb-share" {...mark('smb_share')}>
            {text('smb_share', 'nas-smb-share')}
            {err('smb_share')}
          </FormRow>
          <FormRow label={t('settings.backup.remote.dir')} fieldKey="smb_dir" htmlFor="nas-smb-dir" {...mark('smb_dir')}>
            {text('smb_dir', 'nas-smb-dir')}
            {err('smb_dir')}
            <FieldHelp>{t('settings.backup.remote.dirHelp')}</FieldHelp>
          </FormRow>
          <FormRow label={t('settings.backup.remote.username')} fieldKey="smb_username" htmlFor="nas-smb-user" {...mark('smb_username')}>
            {text('smb_username', 'nas-smb-user')}
            {err('smb_username')}
          </FormRow>
          <FormRow label={t('settings.backup.remote.password')} fieldKey="smb_password · secret" htmlFor="nas-smb-pass" {...mark('smb_password')}>
            <PasswordInput
              id="nas-smb-pass"
              className="max-w-[360px] font-mono"
              autoComplete="new-password"
              showLabel={t('common.show')}
              hideLabel={t('common.hide')}
              placeholder={secretPlaceholder(!!saved?.smb_password.set)}
              value={form.smb_password}
              onChange={(e) => onChange({ smb_password: e.target.value }, ['smb_password'])}
            />
            {err('smb_password')}
          </FormRow>
          <FormRow label={t('settings.backup.remote.domain')} fieldKey="smb_domain" htmlFor="nas-smb-domain" {...mark('smb_domain')}>
            {text('smb_domain', 'nas-smb-domain', { placeholder: t('settings.backup.remote.domainPlaceholder') })}
            {err('smb_domain')}
          </FormRow>
        </>
      ) : (
        <>
          <FormRow label={t('settings.backup.remote.url')} fieldKey="webdav_url" htmlFor="nas-dav-url" {...mark('webdav_url')}>
            <Input
              id="nas-dav-url"
              className="max-w-[440px] font-mono"
              autoComplete="off"
              placeholder={t('settings.backup.remote.urlPlaceholder')}
              value={form.webdav_url}
              invalid={!!errorText('webdav_url')}
              onChange={(e) => onChange({ webdav_url: e.target.value }, ['webdav_url'])}
            />
            {err('webdav_url')}
          </FormRow>
          <FormRow label={t('settings.backup.remote.dir')} fieldKey="webdav_dir" htmlFor="nas-dav-dir" {...mark('webdav_dir')}>
            {text('webdav_dir', 'nas-dav-dir')}
            {err('webdav_dir')}
            <FieldHelp>{t('settings.backup.remote.dirHelp')}</FieldHelp>
          </FormRow>
          <FormRow label={t('settings.backup.remote.username')} fieldKey="webdav_username" htmlFor="nas-dav-user" {...mark('webdav_username')}>
            {text('webdav_username', 'nas-dav-user')}
            {err('webdav_username')}
          </FormRow>
          <FormRow label={t('settings.backup.remote.password')} fieldKey="webdav_password · secret" htmlFor="nas-dav-pass" {...mark('webdav_password')}>
            <PasswordInput
              id="nas-dav-pass"
              className="max-w-[360px] font-mono"
              autoComplete="new-password"
              showLabel={t('common.show')}
              hideLabel={t('common.hide')}
              placeholder={secretPlaceholder(!!saved?.webdav_password.set)}
              value={form.webdav_password}
              onChange={(e) => onChange({ webdav_password: e.target.value }, ['webdav_password'])}
            />
            {err('webdav_password')}
          </FormRow>
        </>
      )}

      <FormRow label={t('settings.backup.remote.passphrase')} fieldKey="passphrase · secret" htmlFor="nas-pass" {...mark('passphrase')}>
        <PasswordInput
          id="nas-pass"
          className="max-w-[360px] font-mono"
          autoComplete="new-password"
          showLabel={t('common.show')}
          hideLabel={t('common.hide')}
          placeholder={saved?.passphrase.set ? t('form.secretKeep') : t('settings.backup.remote.passphraseNew')}
          value={form.passphrase}
          invalid={!!errorText('passphrase')}
          onChange={(e) => onChange({ passphrase: e.target.value }, ['passphrase'])}
        />
        {err('passphrase')}
        <FieldHelp className="text-status-warn">{t('settings.backup.remote.passphraseWarn')}</FieldHelp>
        {passphraseNotice && (
          <Note tone="warn" role="status" className="mt-2">
            {t('settings.backup.remote.passphraseChanged')}
          </Note>
        )}
      </FormRow>
      <FormRow label={t('settings.backup.remote.keep')} fieldKey="keep" htmlFor="nas-keep" {...mark('keep')}>
        <div className="flex items-center gap-2">
          <Input
            id="nas-keep"
            type="number"
            min={1}
            max={365}
            className="w-[110px] font-mono"
            value={form.keep}
            invalid={!!errorText('keep')}
            onChange={(e) => onChange({ keep: Number(e.target.value) }, ['keep'])}
          />
          <span className="text-[12.5px] text-ink-2">{t('settings.backup.remote.keepUnit')}</span>
        </div>
        {err('keep')}
        <FieldHelp>{t('settings.backup.remote.keepHelp')}</FieldHelp>
      </FormRow>
      <FormRow label={t('settings.backup.remote.test')} fieldKey="backup.remote.test">
        <div className="flex flex-wrap items-center gap-3">
          <Button variant="outline" size="sm" className="rounded-[2px]" disabled={test.testing} onClick={onTest}>
            {test.testing ? t('settings.backup.remote.testing') : t('settings.backup.remote.test')}
          </Button>
          {test.result && (
            <span role={test.result.ok ? 'status' : 'alert'} className={test.result.ok ? 'text-[12.5px] text-status-ok' : 'text-[12.5px] text-status-crit'}>
              {test.result.text}
              {test.result.detail && <span className="ml-2 font-mono text-[11px] text-muted-foreground">{test.result.detail}</span>}
            </span>
          )}
        </div>
      </FormRow>
      </fieldset>
    </>
  )
}
