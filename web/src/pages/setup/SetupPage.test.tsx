import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SessionInfo } from '@/api/session'
import { liveStore } from '@/store/live-store'
import { passwordStrength } from '../auth/password-strength'
import { apiError, json, mountAuth, type Handler } from '../auth/auth-test-utils'
import { joinSetupCode, splitSetupCode } from '../auth/setup-code'
import { browserTimezone } from './timezones'

const needsSetup: SessionInfo = { authenticated: false, needs_setup: true }
const CODE = '7K3Q-M9XD-2PLA-8RTE-H4WN-ZC6B'
const PASSWORD = 'pimon-Lab#2026!'

beforeEach(() => {
  localStorage.clear()
  liveStore.reset()
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

function groupInputs() {
  return screen.getAllByLabelText(/^第 \d 组$/) as HTMLInputElement[]
}

async function mountSetup(handler?: Handler, session: () => SessionInfo = () => needsSetup) {
  const user = userEvent.setup()
  const ctx = await mountAuth({ path: '/setup', session, handler })
  await screen.findByRole('heading', { name: '输入设置码' })
  return { user, ...ctx }
}

async function pasteCode(user: ReturnType<typeof userEvent.setup>, text = CODE) {
  await user.click(groupInputs()[0])
  await user.paste(text)
}

async function toStep2(user: ReturnType<typeof userEvent.setup>) {
  await pasteCode(user)
  await user.click(screen.getByRole('button', { name: /下一步/ }))
  await screen.findByRole('heading', { name: '设置管理员密码' })
}

async function toStep3(user: ReturnType<typeof userEvent.setup>, password = PASSWORD) {
  await toStep2(user)
  await user.type(screen.getByLabelText('密码'), password)
  await user.type(screen.getByLabelText('确认密码'), password)
  await user.click(screen.getByRole('button', { name: /下一步/ }))
  await screen.findByRole('heading', { name: '基础设置' })
}

describe('设置码输入', () => {
  it('粘贴整段设置码自动拆分到 6 段（忽略分隔符，转大写）', async () => {
    const { user } = await mountSetup()
    await pasteCode(user, ' 7k3q-m9xd 2pla.8rte-h4wn-zc6b ')
    expect(groupInputs().map((i) => i.value)).toEqual(['7K3Q', 'M9XD', '2PLA', '8RTE', 'H4WN', 'ZC6B'])
  })

  it('逐段输入满 4 位自动跳到下一段，只保留字母数字', async () => {
    const { user } = await mountSetup()
    await user.click(groupInputs()[0])
    await user.keyboard('ab-c1d')
    const v = groupInputs().map((i) => i.value)
    expect(v[0]).toBe('ABC1')
    expect(v[1]).toBe('D')
    expect(groupInputs()[1]).toHaveFocus()
  })

  it('短粘贴含分隔符或空白时不丢字符', async () => {
    const { user } = await mountSetup()
    await pasteCode(user, ' 7K3Q')
    expect(groupInputs()[0].value).toBe('7K3Q')
    expect(groupInputs()[1]).toHaveFocus()
    await user.click(groupInputs()[2])
    await user.paste('7k-3q')
    expect(groupInputs()[2].value).toBe('7K3Q')
  })

  it('空段按退格回到上一段', async () => {
    const { user } = await mountSetup()
    await user.click(groupInputs()[1])
    await user.keyboard('{Backspace}')
    expect(groupInputs()[0]).toHaveFocus()
  })

  it('不足 24 位时不能进入下一步', async () => {
    const { user } = await mountSetup()
    await user.click(groupInputs()[0])
    await user.keyboard('ABCD')
    await user.click(screen.getByRole('button', { name: /下一步/ }))
    expect(await screen.findByText('请输入完整的 24 位设置码')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '输入设置码' })).toBeInTheDocument()
  })

  it('拆分与合并是互逆的纯函数', () => {
    expect(splitSetupCode('abcd efgh')).toEqual(['ABCD', 'EFGH', '', '', '', ''])
    expect(joinSetupCode(splitSetupCode(CODE))).toBe(CODE.replaceAll('-', ''))
    expect(splitSetupCode(CODE + 'EXTRA')).toHaveLength(6)
  })
})

describe('管理员密码', () => {
  it('显示强度，两次输入不一致不能前进', async () => {
    const { user } = await mountSetup()
    await toStep2(user)
    await user.type(screen.getByLabelText('密码'), 'abc')
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuenow', '0')
    await user.clear(screen.getByLabelText('密码'))
    await user.type(screen.getByLabelText('密码'), PASSWORD)
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuenow', '4')
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuetext', '强')
    await user.type(screen.getByLabelText('确认密码'), 'different-1')
    expect(screen.getByText('两次输入不一致')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /下一步/ }))
    expect(screen.getByRole('heading', { name: '设置管理员密码' })).toBeInTheDocument()
    await user.clear(screen.getByLabelText('确认密码'))
    await user.type(screen.getByLabelText('确认密码'), PASSWORD)
    expect(screen.getByText('两次输入一致')).toBeInTheDocument()
  })

  it('不足 8 位给出提示', async () => {
    const { user } = await mountSetup()
    await toStep2(user)
    await user.type(screen.getByLabelText('密码'), 'short')
    await user.type(screen.getByLabelText('确认密码'), 'short')
    await user.click(screen.getByRole('button', { name: /下一步/ }))
    expect(await screen.findByText('密码至少 8 位', { selector: 'p' })).toBeInTheDocument()
  })

  it('强度评分', () => {
    expect(passwordStrength('')).toBe(0)
    expect(passwordStrength('abcdefghij')).toBe(1)
    expect(passwordStrength('abcdefghij12')).toBe(2)
    expect(passwordStrength(PASSWORD)).toBe(4)
  })
})

describe('提交与错误回跳', () => {
  it('默认时区取浏览器时区，提交一次性携带全部字段', async () => {
    const { user, calls } = await mountSetup((c) => (c.url === '/api/setup' ? json(200, { ok: true }) : undefined))
    await toStep3(user)
    expect(screen.getByLabelText('时区')).toHaveValue(browserTimezone())
    await user.type(screen.getByLabelText('访问地址'), 'https://pimon.home.arpa')
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    expect(await screen.findByRole('heading', { name: '设置完成' })).toBeInTheDocument()
    expect(calls.filter((c) => c.url === '/api/setup')).toHaveLength(1)
    expect(calls.find((c) => c.url === '/api/setup')?.body).toEqual({
      setup_code: CODE.replaceAll('-', ''),
      password: PASSWORD,
      language: 'zh',
      timezone: browserTimezone(),
      access_url: 'https://pimon.home.arpa',
    })
    // 完成页上不能被守卫抢先跳走
    expect(screen.getByTestId('loc')).toHaveTextContent(/^\/setup$/)
  })

  it('完成页「进入总览」刷新会话后进入首页', async () => {
    let done = false
    const { user } = await mountSetup(
      (c) => {
        if (c.url === '/api/setup') {
          done = true
          return json(200, { ok: true })
        }
        return undefined
      },
      () => (done ? { authenticated: true, kind: 'admin', needs_setup: false } : needsSetup),
    )
    await toStep3(user)
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    await user.click(await screen.findByRole('button', { name: '进入总览' }))
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent(/^\/$/))
  })

  it('设置码错误回到第一步并显示剩余次数', async () => {
    const { user } = await mountSetup((c) => (c.url === '/api/setup' ? apiError(401, 'setup.invalid_code', { remaining: 3 }) : undefined))
    await toStep3(user)
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    expect(await screen.findByText('设置码错误，还可尝试 3 次')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '输入设置码' })).toBeInTheDocument()
    expect(groupInputs()[0]).toHaveAttribute('aria-invalid', 'true')
    // 输入已保留
    expect(groupInputs().map((i) => i.value).join('')).toBe(CODE.replaceAll('-', ''))
  })

  it('密码字段错误回跳到第二步并高亮', async () => {
    const { user } = await mountSetup((c) =>
      c.url === '/api/setup' ? apiError(400, 'validation.failed', { fields: { password: 'out_of_range', timezone: 'invalid' } }) : undefined,
    )
    await toStep3(user)
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    expect(await screen.findByRole('heading', { name: '设置管理员密码' })).toBeInTheDocument()
    expect(screen.getByLabelText('密码')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText('密码至少 8 位', { selector: '[role="alert"]' })).toBeInTheDocument()
    // 步骤条上标出有错误的步骤（密码、基础设置）
    const steps = within(screen.getByRole('list')).getAllByRole('listitem')
    expect(steps[1]).toHaveAttribute('data-error', 'true')
    expect(steps[2]).toHaveAttribute('data-error', 'true')
    expect(steps[0]).not.toHaveAttribute('data-error')
  })

  it('时区字段错误停留在第三步并高亮时区', async () => {
    const { user } = await mountSetup((c) =>
      c.url === '/api/setup' ? apiError(400, 'validation.failed', { fields: { timezone: 'invalid' } }) : undefined,
    )
    await toStep3(user)
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    expect(await screen.findByText('服务端不识别这个时区，请重新选择')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '基础设置' })).toBeInTheDocument()
    expect(screen.getByLabelText('时区')).toHaveAttribute('aria-invalid', 'true')
  })

  it('未映射的字段错误在第三步兜底显示', async () => {
    const { user } = await mountSetup((c) =>
      c.url === '/api/setup' ? apiError(400, 'validation.failed', { fields: { retention: 'out_of_range' } }) : undefined,
    )
    await toStep3(user)
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    expect(await screen.findByText('retention')).toBeInTheDocument()
    expect(screen.getByText(/超出允许范围/)).toBeInTheDocument()
  })

  it('已完成的步骤可用键盘（按钮）回退', async () => {
    const { user } = await mountSetup()
    await toStep2(user)
    await user.click(screen.getByRole('button', { name: /输入设置码/ }))
    expect(await screen.findByRole('heading', { name: '输入设置码' })).toBeInTheDocument()
  })

  it('访问地址字段错误高亮对应输入框', async () => {
    const { user } = await mountSetup((c) =>
      c.url === '/api/setup' ? apiError(400, 'validation.failed', { fields: { access_url: 'invalid' } }) : undefined,
    )
    await toStep3(user)
    await user.type(screen.getByLabelText('访问地址'), 'ftp://x')
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    await screen.findByText(/访问地址须是/)
    expect(screen.getByLabelText('访问地址')).toHaveAttribute('aria-invalid', 'true')
  })

  it('origin.mismatch 给出反代专门提示', async () => {
    const { user } = await mountSetup((c) => (c.url === '/api/setup' ? apiError(403, 'origin.mismatch') : undefined))
    await toStep3(user)
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    expect(await screen.findByText(/受信任反代/)).toBeInTheDocument()
  })

  it('其他错误按错误码翻译', async () => {
    const { user } = await mountSetup((c) => (c.url === '/api/setup' ? apiError(500, 'internal') : undefined))
    await toStep3(user)
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    expect(await screen.findByText('中枢内部错误')).toBeInTheDocument()
  })

  it('设置码被锁定时显示到期时刻、来源 IP 与倒计时', async () => {
    const { user } = await mountSetup((c) =>
      c.url === '/api/setup'
        ? apiError(429, 'auth.locked', { retry_after_seconds: 900, locked_until: '2026-01-01T12:15:00Z', client_ip: '192.168.1.35' })
        : undefined,
    )
    await toStep3(user)
    await user.click(screen.getByRole('button', { name: /完成设置/ }))
    expect(await screen.findByText(/按来源 IP 192\.168\.1\.35/)).toBeInTheDocument()
    expect(screen.getByTestId('lock-countdown')).toHaveTextContent('剩余 15:00')
    expect(groupInputs()[0]).toBeDisabled()
    await act(async () => {})
  })
})
