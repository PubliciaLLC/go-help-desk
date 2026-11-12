import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'

// #632. The hook that holds passkey registration options so the click that adds
// a passkey can call the browser with nothing to wait for.

vi.mock('@/api/passkeys', async (orig) => {
  const actual = await orig<typeof import('@/api/passkeys')>()
  return { ...actual, prepareRegistration: vi.fn() }
})

import { prepareRegistration, REGISTRATION_REFRESH_MS, type PreparedRegistration } from '@/api/passkeys'
import { useRegistrationOptions } from './useRegistrationOptions'

let n = 0
const prepared = (): PreparedRegistration => ({
  options: { challenge: new Uint8Array([++n]) } as unknown as PublicKeyCredentialCreationOptions,
  fetchedAt: Date.now(),
})

function visibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
}

// Lets the promise the hook is holding settle without moving the clock.
const settle = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })

beforeEach(() => {
  vi.useFakeTimers()
  n = 0
  visibility('visible')
  vi.mocked(prepareRegistration).mockImplementation(async () => prepared())
})
afterEach(() => {
  vi.useRealTimers()
  vi.clearAllMocks()
})

describe('useRegistrationOptions', () => {
  it('fetches once when enabled and hands the options over once', async () => {
    const { result } = renderHook(() => useRegistrationOptions(true))
    await settle()

    expect(prepareRegistration).toHaveBeenCalledTimes(1)
    const first = result.current.take()
    // A plain object, not a promise: the caller can use it in the click.
    expect(first).not.toBeNull()
    expect(first).not.toBeInstanceOf(Promise)
    // Single use: the server has staged one challenge and finish consumes it.
    expect(result.current.take()).toBeNull()
  })

  it('does nothing while disabled', async () => {
    const { result } = renderHook(() => useRegistrationOptions(false))
    await settle()
    expect(prepareRegistration).not.toHaveBeenCalled()
    expect(result.current.take()).toBeNull()
  })

  it('fetches fresh options in the background before the held ones expire', async () => {
    const { result } = renderHook(() => useRegistrationOptions(true))
    await settle()

    await act(async () => { await vi.advanceTimersByTimeAsync(REGISTRATION_REFRESH_MS) })

    expect(prepareRegistration).toHaveBeenCalledTimes(2)
    const held = result.current.take() as PreparedRegistration
    expect(new Uint8Array(held.options.challenge as ArrayBuffer)[0]).toBe(2)
  })

  it('does not hand over options that went stale', async () => {
    const { result } = renderHook(() => useRegistrationOptions(true))
    await settle()
    // Past the lifetime with no refresh in between (a laptop that slept).
    vi.setSystemTime(Date.now() + 10 * 60_000)
    expect(result.current.take()).toBeNull()
  })

  it('hands over the request in flight, so the caller waits for it instead of starting a second', async () => {
    let finish: (p: PreparedRegistration) => void = () => {}
    vi.mocked(prepareRegistration).mockImplementation(() => new Promise((res) => { finish = res }))
    const { result } = renderHook(() => useRegistrationOptions(true))

    const waiting = result.current.take()
    expect(waiting).toBeInstanceOf(Promise)
    expect(prepareRegistration).toHaveBeenCalledTimes(1)
    const p = prepared()
    finish(p)
    await expect(waiting).resolves.toBe(p)
  })

  it('holds nothing and stops asking when the server refuses', async () => {
    vi.mocked(prepareRegistration).mockRejectedValue(new Error('mfa_required'))
    const { result } = renderHook(() => useRegistrationOptions(true))
    await settle()

    expect(result.current.take()).toBeNull()
    await act(async () => { await vi.advanceTimersByTimeAsync(REGISTRATION_REFRESH_MS * 3) })
    expect(prepareRegistration).toHaveBeenCalledTimes(1)
  })

  it('refetches on demand, as after a step-up', async () => {
    vi.mocked(prepareRegistration).mockRejectedValueOnce(new Error('mfa_required'))
    const { result } = renderHook(() => useRegistrationOptions(true))
    await settle()
    expect(result.current.take()).toBeNull()

    act(() => result.current.refresh())
    await settle()

    expect(prepareRegistration).toHaveBeenCalledTimes(2)
    expect(result.current.take()).not.toBeNull()
  })

  it('leaves a hidden tab alone, and catches up when it is shown again', async () => {
    const { result } = renderHook(() => useRegistrationOptions(true))
    await settle()

    visibility('hidden')
    await act(async () => { await vi.advanceTimersByTimeAsync(REGISTRATION_REFRESH_MS) })
    // Nobody is looking, so no request every two minutes for as long as the tab lives.
    expect(prepareRegistration).toHaveBeenCalledTimes(1)

    visibility('visible')
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'))
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(prepareRegistration).toHaveBeenCalledTimes(2)
    expect(result.current.take()).not.toBeNull()
  })

  it('stops its timer when the page goes away', async () => {
    const { unmount } = renderHook(() => useRegistrationOptions(true))
    await settle()
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(REGISTRATION_REFRESH_MS * 2) })
    expect(prepareRegistration).toHaveBeenCalledTimes(1)
  })
})
