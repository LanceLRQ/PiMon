import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SessionInfo } from '@/api/session'
import { liveStore } from '@/store/live-store'
import { apiError, json, mountAuth } from '../auth/auth-test-utils'
import { formatClockTime, formatRemaining, lockInfoOf, makeLock } from '../auth/lock'
import { ApiError } from '@/api/errors'

const anonymous: SessionInfo = { authenticated: false, needs_setup: false }

beforeEach(() => {
  localStorage.clear()
  liveStore.reset()
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('登录页', () => {
  it('密码错误显示剩余次数', async () => {
    const user = userEvent.setup()
    await mountAuth({
      path: '/login',
      session: () => anonymous,
      handler: (c) => (c.url === '/api/login' ? apiError(401, 'auth.invalid_password', { remaining: 7 }) : undefined),
    })
    await user.type(await screen.findByLabelText('管理员密码'), 'hunter2')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByText('密码错误，还可尝试 7 次')).toBeInTheDocument()
    expect(screen.getByLabelText('管理员密码')).toHaveAttribute('aria-invalid', 'true')
  })

  it('英文界面的错误文案同样来自 details', async () => {
    const user = userEvent.setup()
    await mountAuth({
      path: '/login',
      lng: 'en',
      session: () => anonymous,
      handler: (c) => (c.url === '/api/login' ? apiError(401, 'auth.invalid_password', { remaining: 3 }) : undefined),
    })
    await user.type(await screen.findByLabelText(/Admin password/), 'x')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('Wrong password, 3 attempts left')).toBeInTheDocument()
  })

  it('登录成功后回到守卫记住的原页面', async () => {
    const user = userEvent.setup()
    let loggedIn = false
    await mountAuth({
      path: '/login?next=%2Fproxies',
      session: () => (loggedIn ? { authenticated: true, kind: 'admin', needs_setup: false } : anonymous),
      handler: (c) => {
        if (c.url === '/api/login') {
          loggedIn = true
          return json(200, { ok: true })
        }
        return undefined
      },
    })
    await user.type(await screen.findByLabelText('管理员密码'), 'correct-horse')
    await user.click(screen.getByRole('button', { name: '登录' }))
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent(/^\/proxies$/))
  })

  it('提交时携带密码', async () => {
    const user = userEvent.setup()
    const { calls } = await mountAuth({
      path: '/login',
      session: () => anonymous,
      handler: (c) => (c.url === '/api/login' ? apiError(401, 'auth.invalid_password', { remaining: 9 }) : undefined),
    })
    await user.type(await screen.findByLabelText('管理员密码'), 'abc12345{Enter}')
    await screen.findByText('密码错误，还可尝试 9 次')
    expect(calls.find((c) => c.url === '/api/login')?.body).toEqual({ password: 'abc12345' })
  })

  it('origin.mismatch 给出反代专门提示', async () => {
    const user = userEvent.setup()
    await mountAuth({
      path: '/login',
      session: () => anonymous,
      handler: (c) => (c.url === '/api/login' ? apiError(403, 'origin.mismatch') : undefined),
    })
    await user.type(await screen.findByLabelText('管理员密码'), 'x')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByText(/受信任反代/)).toBeInTheDocument()
  })

  it('HTTP 访问时显示明文提示，页脚提示重置密码命令', async () => {
    await mountAuth({ path: '/login', session: () => anonymous })
    expect(await screen.findByText(/当前为 HTTP 连接/)).toBeInTheDocument()
    expect(screen.getByText('pimon-hub reset-password')).toBeInTheDocument()
  })

  it('锁定后显示到期时刻、来源 IP 与倒计时，到期后恢复输入', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] })
    vi.setSystemTime(new Date('2026-01-01T12:00:00Z'))
    const lockedUntil = '2026-01-01T12:15:00Z'
    await mountAuth({
      path: '/login',
      session: () => anonymous,
      handler: (c) =>
        c.url === '/api/login'
          ? apiError(429, 'auth.locked', { retry_after_seconds: 900, locked_until: lockedUntil, client_ip: '192.168.1.35' })
          : undefined,
    })
    const input = await screen.findByLabelText('管理员密码')
    fireInput(input, 'x')
    await act(async () => {
      screen.getByRole('button', { name: '登录' }).click()
    })
    const time = formatClockTime(Date.parse(lockedUntil), 'zh')
    expect(await screen.findByText(`${time} 后可再试（按来源 IP 192.168.1.35）`, { exact: false })).toBeInTheDocument()
    expect(screen.getByTestId('lock-countdown')).toHaveTextContent('剩余 15:00')
    expect(screen.getByLabelText('管理员密码')).toBeDisabled()
    expect(screen.getByRole('button', { name: '登录' })).toBeDisabled()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(61_000)
    })
    expect(screen.getByTestId('lock-countdown')).toHaveTextContent('剩余 13:59')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(14 * 60_000)
    })
    expect(screen.queryByTestId('lock-countdown')).not.toBeInTheDocument()
    expect(screen.getByLabelText('管理员密码')).toBeEnabled()
  })
})

// 直接设置受控输入框的值（fake timers 下 userEvent 的输入会被计时器卡住）
function fireInput(el: HTMLElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  act(() => {
    setter.call(el, value)
    el.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

describe('锁定信息解析', () => {
  const locked = (details: Record<string, unknown>) => new ApiError(429, 'auth.locked', details)

  it('以 retry_after_seconds 为剩余时长，locked_until 仅用于显示', () => {
    const info = lockInfoOf(locked({ retry_after_seconds: 900, locked_until: '2026-01-01T12:15:00Z', client_ip: '10.0.0.2' }), 0)
    expect(info).toEqual({ remainingMs: 900_000, untilMs: Date.parse('2026-01-01T12:15:00Z'), clientIp: '10.0.0.2' })
  })

  it('只有 locked_until 时按当前时间推算剩余', () => {
    const now = Date.parse('2026-01-01T12:00:00Z')
    const info = lockInfoOf(locked({ locked_until: '2026-01-01T12:01:30Z' }), now)
    expect(info?.remainingMs).toBe(90_000)
    expect(info?.clientIp).toBeNull()
  })

  it('其他错误码返回 null', () => {
    expect(lockInfoOf(new ApiError(401, 'auth.invalid_password', { remaining: 1 }))).toBeNull()
    expect(lockInfoOf(new Error('x'))).toBeNull()
  })

  it('倒计时与截止时刻格式', () => {
    expect(formatRemaining(900_000)).toBe('15:00')
    expect(formatRemaining(61_001)).toBe('01:02')
    const lock = makeLock({ remainingMs: 5000, untilMs: 0, clientIp: null }, 1000)
    expect(lock.deadline).toBe(6000)
  })
})
