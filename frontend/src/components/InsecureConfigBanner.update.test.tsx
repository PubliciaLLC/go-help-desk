import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// #170: when the opt-in update check has found a newer release, administrators
// (only) get one info notice in the server notice bar. It is information, not a
// problem: it never turns the bar red, is announced politely, and its Hide is
// remembered per version in localStorage (the bar's own hides last a session).

const update = { current: '1.3.0', latest: '1.4.0', url: 'https://github.com/PubliciaLLC/go-help-desk/releases/tag/v1.4.0' }
const TITLE = 'Version 1.4.0 is available (you run 1.3.0)'
const PASSKEYS = { available: false, reason: 'must be a domain' }

function serve(extra: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') {
      return Promise.resolve({
        data: {
          insecure_secrets: [],
          attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '', policy_explicit: false, stale_off: false },
          ...extra,
        },
      })
    }
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  localStorage.clear()
  sessionStorage.clear()
})
afterEach(() => vi.restoreAllMocks())

describe('the new release notice in the bar', () => {
  it('is a lone info notice, announced politely as a status and not an alert', async () => {
    serve({ update })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const status = await screen.findByRole('status')
    expect(status.textContent).toContain(TITLE)
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('names both versions and links to the release in a new tab', async () => {
    serve({ update })
    const user = userEvent.setup()
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await screen.findByRole('status')
    await user.click(screen.getByRole('button', { name: /details/i }))
    const row = screen.getByRole('link', { name: /release notes/i }).closest('li')!
    expect(row.textContent).toContain('Version 1.4.0 is available (you run 1.3.0).')
    const link = screen.getByRole('link', { name: /release notes/i }) as HTMLAnchorElement
    expect(link.href).toBe(update.url)
    expect(link.target).toBe('_blank')
    expect(link.rel).toBe('noopener noreferrer')
  })

  it('does not count as a problem next to one, and does not make the bar an alert by itself', async () => {
    serve({ update, passkeys: PASSKEYS })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('Passkeys are unavailable')
    expect(alert.textContent).not.toMatch(/2 server problems/)
  })

  it('is absent when the server reports no update', async () => {
    serve({})
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(api.get).toHaveBeenCalled())
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('is never shown to, or requested for, a non-admin', () => {
    serve({ update })
    renderWithQuery(<InsecureConfigBanner isAdmin={false} />)
    expect(api.get).not.toHaveBeenCalled()
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('Hide is remembered for that version across a reload, not just the session', async () => {
    serve({ update })
    const user = userEvent.setup()
    const first = renderWithQuery(<InsecureConfigBanner isAdmin />)
    await screen.findByRole('status')
    await user.click(screen.getByRole('button', { name: /details/i }))
    await user.click(screen.getByRole('button', { name: /hide for this session: version 1\.4\.0/i }))
    expect(screen.queryByRole('status')).toBeNull()
    expect(localStorage.getItem('ghd.update-notice-dismissed')).toBe('1.4.0')
    first.unmount()

    // A new tab or sign-in: sessionStorage is gone, localStorage is not.
    sessionStorage.clear()
    const before = vi.mocked(api.get).mock.calls.length
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(vi.mocked(api.get).mock.calls.length).toBeGreaterThan(before))
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('comes back for a newer release than the one hidden', async () => {
    localStorage.setItem('ghd.update-notice-dismissed', '1.4.0')
    serve({ update: { ...update, latest: '1.5.0', url: update.url.replace('1.4.0', '1.5.0') } })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect((await screen.findByRole('status')).textContent).toContain('Version 1.5.0 is available')
  })

  it('is keyed update:<version> so each release is its own notice', async () => {
    serve({ update })
    const user = userEvent.setup()
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await screen.findByRole('status')
    await user.click(screen.getByRole('button', { name: /details/i }))
    await user.click(screen.getByRole('button', { name: /hide for this session/i }))
    expect(sessionStorage.key(0)).toContain('update:1.4.0')
  })

  it('works when storage is blocked: hidden for this page view only', async () => {
    serve({ update })
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect((await screen.findByRole('status')).textContent).toContain(TITLE)
  })
})
