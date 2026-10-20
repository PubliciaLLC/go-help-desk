import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { focusManager } from '@tanstack/react-query'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'
import { clearDismissedNotices } from '@/lib/noticeDismissal'

// One slim bar replaces the stack of banners: a summary line, details on
// request, a per-session dismiss, red only for what is broken now.

const STORAGE = { writable: false, problem: 'permission denied' }
const PASSKEYS = { available: false, reason: 'must be a domain and not an IP address' }
const QUIET = { insecure_secrets: [], attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '' } }

function mockWarnings(extra: Record<string, unknown>) {
  const payload: { current: Record<string, unknown> } = { current: { ...QUIET, ...extra } }
  const spy = vi.spyOn(api, 'get').mockImplementation(((url: string) =>
    Promise.resolve({ data: url === '/admin/security-warnings' ? payload.current : {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ) as any)
  return { payload, spy }
}

const calls = (spy: ReturnType<typeof vi.spyOn>) =>
  spy.mock.calls.filter((c: unknown[]) => c[0] === '/admin/security-warnings').length

beforeEach(() => sessionStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe('the slim bar', () => {
  it('renders nothing for zero notices', async () => {
    const { spy } = mockWarnings({})
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(calls(spy)).toBe(1))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
    expect(screen.queryByRole('button', { name: /details/i })).toBeNull()
  })

  it('names the one notice and offers Details', async () => {
    mockWarnings({ passkeys: PASSKEYS })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const bar = await screen.findByRole('region', { name: 'Server notices' })
    expect(bar.textContent).toContain('Passkeys are unavailable')
    expect(screen.getByRole('button', { name: /details/i })).toBeTruthy()
  })

  it('counts two notices', async () => {
    mockWarnings({ passkeys: PASSKEYS, attachment_storage: STORAGE })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect((await screen.findByRole('region', { name: 'Server notices' })).textContent).toContain('2 server problems need attention')
  })

  it('expands to the full text, with aria-expanded following', async () => {
    mockWarnings({ passkeys: PASSKEYS, attachment_storage: STORAGE })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await screen.findByRole('region', { name: 'Server notices' })
    const details = screen.getByRole('button', { name: /details/i })
    expect(details.getAttribute('aria-expanded')).toBe('false')
    const panel = document.getElementById('server-notices')!
    expect(panel.hidden).toBe(true)

    fireEvent.click(details)
    expect(details.getAttribute('aria-expanded')).toBe('true')
    expect(panel.hidden).toBe(false)
    expect(screen.getByText(/reset-factors <email>/)).toBeTruthy()
    expect(screen.getByText(/The server cannot write to its attachment directory \(permission denied\)/)).toBeTruthy()

    fireEvent.click(details)
    expect(details.getAttribute('aria-expanded')).toBe('false')
  })

  it('wraps long code instead of widening the page', async () => {
    mockWarnings({ passkeys: PASSKEYS })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    expect(screen.getByText(/reset-factors/).className).toContain('overflow-wrap:anywhere')
    expect((await screen.findByRole('region', { name: 'Server notices' })).className).toContain('overflow-wrap:anywhere')
  })
})

describe('severity', () => {
  it('is red for a real failure', async () => {
    mockWarnings({ attachment_storage: STORAGE })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect((await screen.findByRole('region', { name: 'Server notices' })).className).toContain('bg-red-')
  })

  // Required refuses uploads; permissive stores them unscanned right now.
  // Either way the scanner is failing, so either way it is red.
  it.each(['required', 'permissive'])('is red for an unreachable scanner under the %s policy', async (policy) => {
    mockWarnings({ attachment_scanning: { policy, configured: true, reachable: false, effect: 'x' } })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const bar = await screen.findByRole('region', { name: 'Server notices' })
    expect(bar.className).toContain('bg-red-')
    expect(bar.className).not.toContain('bg-amber-')
  })

  it('is neutral for a configuration notice', async () => {
    mockWarnings({ passkeys: PASSKEYS })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const bar = await screen.findByRole('region', { name: 'Server notices' })
    expect(bar.className).toContain('bg-amber-')
    expect(bar.className).not.toContain('bg-red-')
  })

  it('goes red when a failure sits beside a configuration notice', async () => {
    mockWarnings({ passkeys: PASSKEYS, attachment_storage: STORAGE })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect((await screen.findByRole('region', { name: 'Server notices' })).className).toContain('bg-red-')
  })
})

describe('dismissing', () => {
  it('hides one notice for the session and keeps the other', async () => {
    mockWarnings({ passkeys: PASSKEYS, attachment_storage: STORAGE })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    fireEvent.click(screen.getByRole('button', { name: /hide for this session: passkeys are unavailable/i }))

    const bar = screen.getByRole('region', { name: 'Server notices' })
    expect(bar.textContent).not.toContain('Passkeys are unavailable')
    expect(bar.textContent).toContain('Attachment uploads are disabled')
    expect(bar.textContent).not.toContain('server problems')
  })

  it('removes the bar when every notice is dismissed', async () => {
    mockWarnings({ passkeys: PASSKEYS })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    fireEvent.click(screen.getByRole('button', { name: /hide for this session/i }))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })

  it('stays hidden on a remount in the same session, and nothing is sent to the server', async () => {
    const { spy } = mockWarnings({ passkeys: PASSKEYS })
    const first = renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    fireEvent.click(screen.getByRole('button', { name: /hide for this session/i }))
    first.unmount()
    expect(spy.mock.calls.every((c: unknown[]) => c[0] === '/admin/security-warnings')).toBe(true)
    const before = calls(spy)

    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(calls(spy)).toBe(before + 1))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })

  // A new session is an empty sessionStorage: the problem is still there, so
  // the notice is too. A failure is never hidden for good.
  it('comes back in a new session while the problem remains', async () => {
    mockWarnings({ attachment_storage: STORAGE })
    const first = renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    fireEvent.click(screen.getByRole('button', { name: /hide for this session/i }))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
    first.unmount()

    sessionStorage.clear() // the new session
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect((await screen.findByRole('region', { name: 'Server notices' })).textContent).toContain('Attachment uploads are disabled')
  })

  it('comes back after sign-out even though the tab survives it', async () => {
    mockWarnings({ passkeys: PASSKEYS })
    const first = renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    fireEvent.click(screen.getByRole('button', { name: /hide for this session/i }))
    first.unmount()

    clearDismissedNotices()
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect(await screen.findByRole('region', { name: 'Server notices' })).toBeTruthy()
  })
})

describe('who sees it', () => {
  it('shows a non-admin nothing and asks nothing', () => {
    const { spy } = mockWarnings({ passkeys: PASSKEYS, attachment_storage: STORAGE })
    renderWithQuery(<InsecureConfigBanner isAdmin={false} />)
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
    expect(spy).not.toHaveBeenCalled()
  })
})

// A fixed problem must stop showing within seconds of the admin returning to
// the tab, not after the five-minute stale window.
describe('freshness', () => {
  function refocus() {
    act(() => {
      focusManager.setFocused(false)
      focusManager.setFocused(true)
    })
  }

  it('refetches on window focus and drops a problem that was fixed', async () => {
    const { payload, spy } = mockWarnings({ attachment_storage: STORAGE })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await screen.findByRole('region', { name: 'Server notices' })
    expect(calls(spy)).toBe(1)

    payload.current = { ...QUIET, attachment_storage: { writable: true, problem: '' } }
    refocus()
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull())
    expect(calls(spy)).toBe(2)
  })

  it('refetches when Details is opened, and only then', async () => {
    const { payload, spy } = mockWarnings({ attachment_storage: STORAGE })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const details = await screen.findByRole('button', { name: /details/i })
    expect(calls(spy)).toBe(1)

    payload.current = { ...QUIET, attachment_storage: { writable: true, problem: '' } }
    fireEvent.click(details)
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull())
    expect(calls(spy)).toBe(2)
  })

  it('does not refetch when Details is closed again', async () => {
    const { spy } = mockWarnings({ passkeys: PASSKEYS })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const details = await screen.findByRole('button', { name: /details/i })
    fireEvent.click(details)
    await waitFor(() => expect(calls(spy)).toBe(2))
    fireEvent.click(details)
    expect(calls(spy)).toBe(2)
  })
})

// A notice is hidden by what it said when it was hidden. If it gets worse it
// is a different notice and comes back.
describe('a notice that gets worse comes back', () => {
  async function hideThenChange(first: Record<string, unknown>, second: Record<string, unknown>) {
    const { payload } = mockWarnings(first)
    const one = renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    fireEvent.click(screen.getByRole('button', { name: /hide for this session/i }))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
    one.unmount()

    payload.current = { ...QUIET, ...second }
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    return screen.findByRole('region', { name: 'Server notices' })
  }

  const down = { configured: true, reachable: false, effect: 'x' }

  it('when the scanner policy changes', async () => {
    const bar = await hideThenChange(
      { attachment_scanning: { policy: 'required', ...down } },
      { attachment_scanning: { policy: 'permissive', ...down } },
    )
    expect(bar).toBeTruthy()
  })

  it('when more email fails', async () => {
    const email = { enabled: true, failing: true, failing_count: 1, last_error: 'x', missing: [] }
    const bar = await hideThenChange({ email }, { email: { ...email, failing_count: 5 } })
    expect(bar.textContent).toContain('5 messages have failed')
  })

  it('when another OAuth client is blind', async () => {
    const bar = await hideThenChange(
      { oauth_clients_without_groups: ['A'] },
      { oauth_clients_without_groups: ['A', 'B'] },
    )
    expect(bar.textContent).toContain('A, B')
  })

  it('when a second insecure secret appears', async () => {
    const bar = await hideThenChange({ insecure_secrets: ['SESSION_SECRET'] }, { insecure_secrets: ['SESSION_SECRET', 'CSRF_KEY'] })
    expect(bar.textContent).toContain('SESSION_SECRET and CSRF_KEY')
  })

  it('but not when nothing changed', async () => {
    const { payload } = mockWarnings({ oauth_clients_without_groups: ['A'] })
    const one = renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    fireEvent.click(screen.getByRole('button', { name: /hide for this session/i }))
    one.unmount()
    payload.current = { ...QUIET, oauth_clients_without_groups: ['A'] }
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull())
  })
})

describe('accessibility', () => {
  it('announces only the summary line; the container is a labelled region', async () => {
    mockWarnings({ passkeys: PASSKEYS, attachment_storage: STORAGE })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('2 server problems need attention')
    expect(alert.textContent).not.toContain('reset-factors')
    expect(alert.textContent).not.toContain('Details')
    expect(screen.getByRole('region', { name: 'Server notices' }).contains(alert)).toBe(true)
  })

  it('moves focus to the next Hide button after hiding, and to Details after the last', async () => {
    mockWarnings({ passkeys: PASSKEYS, attachment_storage: STORAGE, oauth_clients_without_groups: ['A'] })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    const hides = () => screen.getAllByRole('button', { name: /hide for this session/i })
    expect(hides()).toHaveLength(3)

    // Notices render in a fixed order; hide the first and the one that was
    // second is now first and holds focus.
    const second = hides()[1].getAttribute('aria-label')
    hides()[0].focus()
    fireEvent.click(hides()[0])
    await waitFor(() => expect(document.activeElement?.getAttribute('aria-label')).toBe(second))

    const last = hides()[1]
    last.focus()
    fireEvent.click(last)
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: /details/i })))
  })

  it('makes Hide a 44px target on touch screens', async () => {
    mockWarnings({ passkeys: PASSKEYS })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    expect(screen.getByRole('button', { name: /hide for this session/i }).className).toContain('pointer-coarse:min-h-11')
  })
})
