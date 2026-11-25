import { act, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ThemeController } from '@/components/theme/ThemeController'
import { updateAppearance } from '@/api/preferences'
import type { ThemePreference, User } from '@/api/types'
import { THEME_KEY } from '@/lib/theme'
import { useAuthStore } from './auth'
import { SAVE_ERROR, useThemeStore } from './theme'

// Which theme a page uses (#709, DESIGN.md "Which theme a page uses"): the
// account's value wins when signed in and is mirrored to localStorage; before
// sign-in the stored value is used; signing out keeps it. The controller is
// mounted because the auth, storage and media-query wiring is part of the rule.

vi.mock('@/api/preferences', () => ({ updateAppearance: vi.fn() }))

const html = document.documentElement
const isDark = () => html.classList.contains('dark')
const stored = () => window.localStorage.getItem(THEME_KEY)

function user(theme?: ThemePreference): User {
  return {
    id: 'u-1',
    email: 'sam@example.com',
    display_name: 'Sam',
    role: 'staff',
    mfa_enabled: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...(theme ? { theme } : {}),
  }
}

function systemDark(initial: boolean) {
  let listener: ((e: { matches: boolean }) => void) | null = null
  const mq = {
    matches: initial,
    media: '(prefers-color-scheme: dark)',
    addEventListener: (_: string, cb: (e: { matches: boolean }) => void) => { listener = cb },
    removeEventListener: () => { listener = null },
  }
  window.matchMedia = vi.fn().mockReturnValue(mq) as unknown as typeof window.matchMedia
  return {
    change: (matches: boolean) => act(() => { mq.matches = matches; listener?.({ matches }) }),
    listening: () => listener !== null,
  }
}

const originalMatchMedia = window.matchMedia

beforeEach(() => {
  window.localStorage.clear()
  html.classList.remove('dark')
  window.matchMedia = originalMatchMedia
  useAuthStore.setState({ user: null })
  useThemeStore.setState({ preference: 'device', prefersDark: false, saving: false, pending: 0, error: null, saveCount: 0 })
})
afterEach(() => {
  vi.restoreAllMocks()
  window.matchMedia = originalMatchMedia
})

describe('the first preference', () => {
  async function fresh() {
    vi.resetModules()
    return (await import('./theme')).useThemeStore.getState().preference
  }

  it('is the stored value', async () => {
    window.localStorage.setItem(THEME_KEY, 'dark')
    expect(await fresh()).toBe('dark')
  })

  it('is device when nothing is stored', async () => {
    expect(await fresh()).toBe('device')
  })

  it('is device when the stored value is not one of the three', async () => {
    window.localStorage.setItem(THEME_KEY, 'auto')
    expect(await fresh()).toBe('device')
  })

  it('is device when storage refuses', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new DOMException('denied', 'SecurityError') })
    expect(await fresh()).toBe('device')
  })
})

describe('choosing before sign-in', () => {
  it('applies and remembers the choice, and sends nothing', async () => {
    render(<ThemeController />)
    await act(() => useThemeStore.getState().setPreference('dark'))

    expect(isDark()).toBe(true)
    expect(stored()).toBe('dark')
    expect(updateAppearance).not.toHaveBeenCalled()
    expect(useThemeStore.getState().saving).toBe(false)
  })

  it('still applies when storage refuses the write', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('full', 'QuotaExceededError') })
    render(<ThemeController />)
    await act(() => useThemeStore.getState().setPreference('dark'))
    expect(isDark()).toBe(true)
  })
})

describe('choosing while signed in', () => {
  beforeEach(() => useAuthStore.setState({ user: user('device') }))

  it('applies at once, sends the choice, and the auth store then carries it', async () => {
    vi.mocked(updateAppearance).mockResolvedValue(user('dark'))
    render(<ThemeController />)
    const done = act(() => useThemeStore.getState().setPreference('dark'))

    expect(isDark()).toBe(true) // before the request has answered
    expect(useThemeStore.getState().saving).toBe(true)
    await done

    expect(updateAppearance).toHaveBeenCalledWith('dark')
    expect(useAuthStore.getState().user?.theme).toBe('dark')
    expect(useThemeStore.getState().saving).toBe(false)
    expect(useThemeStore.getState().error).toBeNull()
    expect(stored()).toBe('dark')
  })

  it('puts the previous value back, on screen and in storage, and says so when the save fails', async () => {
    vi.mocked(updateAppearance).mockRejectedValue(new Error('boom'))
    render(<ThemeController />)
    act(() => useAuthStore.getState().setUser(user('light')))
    expect(stored()).toBe('light')
    await act(() => useThemeStore.getState().setPreference('dark'))

    expect(isDark()).toBe(false)
    expect(stored()).toBe('light')
    expect(useThemeStore.getState().preference).toBe('light')
    expect(useThemeStore.getState().error).toBe("Couldn't save your appearance. Try again.")
    expect(SAVE_ERROR).toBe("Couldn't save your appearance. Try again.")
    expect(useThemeStore.getState().saving).toBe(false)
  })

  it('forgets an old error when the next choice starts', async () => {
    useThemeStore.setState({ error: SAVE_ERROR })
    vi.mocked(updateAppearance).mockResolvedValue(user('dark'))
    render(<ThemeController />)
    await act(() => useThemeStore.getState().setPreference('dark'))
    expect(useThemeStore.getState().error).toBeNull()
  })

  it('keeps the newer choice when an older save fails after it', async () => {
    let failFirst: (e: Error) => void = () => {}
    vi.mocked(updateAppearance)
      .mockImplementationOnce(() => new Promise((_, rej) => { failFirst = rej }))
      .mockResolvedValueOnce(user('light'))
    render(<ThemeController />)
    const first = useThemeStore.getState().setPreference('dark')
    await act(() => useThemeStore.getState().setPreference('light'))
    await act(async () => { failFirst(new Error('late')); await first })

    expect(useThemeStore.getState().preference).toBe('light')
    expect(isDark()).toBe(false)
    expect(useThemeStore.getState().saving).toBe(false)
  })

  it('does not let an older /me answer overwrite a choice that is still being saved', async () => {
    let finish: (u: User) => void = () => {}
    vi.mocked(updateAppearance).mockImplementation(() => new Promise((res) => { finish = res }))
    render(<ThemeController />)
    const saving = useThemeStore.getState().setPreference('dark')

    act(() => useAuthStore.getState().setUser(user('device'))) // stale answer from elsewhere
    expect(useThemeStore.getState().preference).toBe('dark')
    expect(isDark()).toBe(true)

    await act(async () => { finish(user('dark')); await saving })
    expect(useThemeStore.getState().preference).toBe('dark')
  })
})

describe('signing in', () => {
  it("the account's value replaces what this browser had, and is stored", () => {
    window.localStorage.setItem(THEME_KEY, 'dark')
    useThemeStore.setState({ preference: 'dark' })
    html.classList.add('dark')
    render(<ThemeController />)
    act(() => useAuthStore.getState().setUser(user('device')))

    expect(useThemeStore.getState().preference).toBe('device')
    expect(stored()).toBe('device')
    expect(isDark()).toBe(false) // the system is light
  })

  it("applies an account's dark to a page that was light", () => {
    render(<ThemeController />)
    act(() => useAuthStore.getState().setUser(user('dark')))
    expect(isDark()).toBe(true)
    expect(stored()).toBe('dark')
  })

  it('applies the account value already in the store when the controller mounts', () => {
    useAuthStore.setState({ user: user('dark') })
    render(<ThemeController />)
    expect(isDark()).toBe(true)
  })

  it('keeps the local value when the server does not say (an older server)', () => {
    window.localStorage.setItem(THEME_KEY, 'dark')
    useThemeStore.setState({ preference: 'dark' })
    render(<ThemeController />)
    act(() => useAuthStore.getState().setUser(user()))

    expect(useThemeStore.getState().preference).toBe('dark')
    expect(stored()).toBe('dark')
  })
})

describe('signing out', () => {
  it('keeps the preference and the stored value', () => {
    render(<ThemeController />)
    act(() => useAuthStore.getState().setUser(user('dark')))
    act(() => useAuthStore.getState().clear())

    expect(useThemeStore.getState().preference).toBe('dark')
    expect(stored()).toBe('dark')
    expect(isDark()).toBe(true)
  })
})

describe('another tab', () => {
  function storageEvent(init: StorageEventInit) {
    act(() => { window.dispatchEvent(new StorageEvent('storage', init)) })
  }

  it('is followed without a request or a write', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    render(<ThemeController />)
    storageEvent({ key: THEME_KEY, newValue: 'dark' })

    expect(useThemeStore.getState().preference).toBe('dark')
    expect(isDark()).toBe(true)
    expect(updateAppearance).not.toHaveBeenCalled()
    expect(setItem).not.toHaveBeenCalled()
  })

  it('is ignored when the value is not one of the three', () => {
    render(<ThemeController />)
    storageEvent({ key: THEME_KEY, newValue: 'x' })
    expect(useThemeStore.getState().preference).toBe('device')
  })

  it('is ignored when the storage was cleared (key null)', () => {
    useThemeStore.setState({ preference: 'dark' })
    render(<ThemeController />)
    storageEvent({ key: null, newValue: null })
    expect(useThemeStore.getState().preference).toBe('dark')
  })

  it('is ignored for another key', () => {
    render(<ThemeController />)
    storageEvent({ key: 'ghd.admin-nav-open', newValue: 'dark' })
    expect(useThemeStore.getState().preference).toBe('device')
  })
})

describe('the device setting', () => {
  it('follows the system live while the preference is device', () => {
    const mq = systemDark(false)
    render(<ThemeController />)
    expect(isDark()).toBe(false)

    mq.change(true)
    expect(isDark()).toBe(true)
    mq.change(false)
    expect(isDark()).toBe(false)
  })

  it('does not follow the system when the choice is light or dark', () => {
    const mq = systemDark(false)
    useThemeStore.setState({ preference: 'light' })
    render(<ThemeController />)
    mq.change(true)
    expect(isDark()).toBe(false)

    act(() => { void useThemeStore.getState().setPreference('dark') })
    mq.change(false)
    expect(isDark()).toBe(true)
  })

  it('starts dark when the system is dark at mount', () => {
    systemDark(true)
    render(<ThemeController />)
    expect(isDark()).toBe(true)
  })

  it('is light in a browser without matchMedia', () => {
    // @ts-expect-error the browser this models has no matchMedia
    window.matchMedia = undefined
    render(<ThemeController />)
    expect(isDark()).toBe(false)
  })

  it('stops listening when the controller unmounts', () => {
    const mq = systemDark(false)
    const { unmount } = render(<ThemeController />)
    expect(mq.listening()).toBe(true)
    unmount()
    expect(mq.listening()).toBe(false)
  })
})
