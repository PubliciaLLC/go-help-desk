import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// "Require SAML for staff and users" on while SAML is not loaded: staff and
// users cannot sign in. The server reports it as saml_required_not_loaded, for
// as long as it is true (docs/DESIGN.md, SAML, "A server problem while
// saml_enabled is on and SAML is not loaded").

const QUIET = { insecure_secrets: [], attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '' } }
const TITLE = 'Staff and users cannot sign in: SAML is required but not set up'

function mockWarnings(extra: Record<string, unknown>) {
  const payload: { current: Record<string, unknown> } = { current: { ...QUIET, ...extra } }
  vi.spyOn(api, 'get').mockImplementation(((url: string) =>
    Promise.resolve({ data: url === '/admin/security-warnings' ? payload.current : {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ) as any)
  return payload
}

beforeEach(() => sessionStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe('the SAML required but not loaded notice', () => {
  it('is a red notice that names the problem in the bar', async () => {
    mockWarnings({ saml_required_not_loaded: { reason: 'incomplete' } })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const bar = await screen.findByRole('region', { name: 'Server notices' })
    expect(bar.textContent).toContain(TITLE)
    expect(bar.className).toContain('bg-red-')
    expect(screen.getByRole('button', { name: /details/i })).toBeTruthy()
  })

  const reasons = [
    { reason: 'incomplete', says: 'metadata, the certificate or the private key is missing' },
    { reason: 'not_loaded', says: 'metadata could not be loaded' },
  ]
  it.each(reasons)('says what to do and links to the Authentication page for reason $reason', async ({ reason, says }) => {
    mockWarnings({ saml_required_not_loaded: { reason } })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    const text = screen.getByRole('region', { name: 'Server notices' }).textContent ?? ''
    expect(text).toContain('Require SAML for staff and users')
    expect(text).toContain(says)
    expect(text).toContain('password sign-in is refused')
    expect(text).toContain('Administrators can still sign in with a password')
    expect(text).toContain('turn off')
    expect(text).toContain('goes away by itself')
    const link = screen.getByRole('link', { name: /System → Authentication → SAML/ })
    expect(link.getAttribute('href')).toBe('/admin/settings/authentication')
  })

  it('counts with the other problems', async () => {
    mockWarnings({
      saml_required_not_loaded: { reason: 'incomplete' },
      attachment_storage: { writable: false, problem: 'permission denied' },
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect((await screen.findByRole('region', { name: 'Server notices' })).textContent).toContain('2 server problems need attention')
  })

  it('says nothing when the server sends no such field', async () => {
    mockWarnings({})
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })

  it('can be hidden for the session, and comes back when the reason changes', async () => {
    const payload = mockWarnings({ saml_required_not_loaded: { reason: 'incomplete' } })
    const first = renderWithQuery(<InsecureConfigBanner isAdmin />)
    fireEvent.click(await screen.findByRole('button', { name: /details/i }))
    fireEvent.click(screen.getByRole('button', { name: /hide for this session/i }))
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull())
    first.unmount()

    // Same problem, still hidden.
    const before = vi.mocked(api.get).mock.calls.length
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(vi.mocked(api.get).mock.calls.length).toBeGreaterThan(before))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()

    // A different state of the problem is a new notice.
    payload.current = { ...QUIET, saml_required_not_loaded: { reason: 'not_loaded' } }
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect((await screen.findByRole('region', { name: 'Server notices' })).textContent).toContain(TITLE)
  })

  it('does not ask a non-administrator', async () => {
    mockWarnings({ saml_required_not_loaded: { reason: 'incomplete' } })
    renderWithQuery(<InsecureConfigBanner isAdmin={false} />)
    await new Promise((r) => setTimeout(r, 20))
    expect(api.get).not.toHaveBeenCalled()
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })
})
