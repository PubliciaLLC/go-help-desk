import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// #170: when the opt-in update check has found a newer release, administrators
// (only) see a small notice they can dismiss.

const update = { current: '1.3.0', latest: '1.4.0', url: 'https://github.com/PubliciaLLC/go-help-desk/releases/tag/v1.4.0' }

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

beforeEach(() => localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe('the new release notice', () => {
  it('names both versions and links to the release', async () => {
    serve({ update })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const note = await screen.findByRole('status')
    expect(note.textContent).toContain('Version 1.4.0 is available (you run 1.3.0)')
    const link = screen.getByRole('link', { name: /release notes/i }) as HTMLAnchorElement
    expect(link.href).toBe(update.url)
    expect(link.rel).toContain('noopener')
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

  it('can be dismissed, and stays dismissed for that version only', async () => {
    serve({ update })
    const user = userEvent.setup()
    const first = renderWithQuery(<InsecureConfigBanner isAdmin />)
    await screen.findByRole('status')
    await user.click(screen.getByRole('button', { name: /dismiss/i }))
    expect(screen.queryByRole('status')).toBeNull()
    first.unmount()

    // Remounted (a reload): still dismissed.
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('comes back for a newer release than the one dismissed', async () => {
    localStorage.setItem('ghd.update-notice-dismissed', '1.4.0')
    serve({ update: { ...update, latest: '1.5.0', url: update.url.replace('1.4.0', '1.5.0') } })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect((await screen.findByRole('status')).textContent).toContain('Version 1.5.0 is available')
  })
})
