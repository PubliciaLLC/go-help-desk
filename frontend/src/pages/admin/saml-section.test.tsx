import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The SAML section of System -> Authentication. "Save SAML config" is what
// switches SAML on, so its form is always shown. saml_enabled is a different
// thing, "staff and users must use SAML", saved by the page's own Save; a
// settings refetch (after the SAML save, or the window regaining focus) must not
// undo a switch that was set and not yet saved.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings/authentication' } }),
  useBlocker: () => ({ status: 'idle' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { AuthenticationSettingsPage } from './settings/AuthenticationSettingsPage'

const ENTITY_ID = 'https://hd.example.com/api/v1/auth/saml/metadata'
const ACS = 'https://hd.example.com/api/v1/auth/saml/acs'
const TOGGLE = 'Require SAML for staff and users'

let settings: Record<string, unknown>
let samlConfig: Record<string, unknown>

beforeEach(() => {
  settings = { saml_enabled: false, saml_metadata_url: '' }
  samlConfig = {
    configured: false,
    live: false,
    metadata_url: '',
    metadata_xml: '',
    cert_pem: '',
    sp_metadata_url: ENTITY_ID,
    sp_acs_url: ACS,
  }
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'admin@example.com',
      display_name: 'Ada Admin',
      role: 'admin',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: { ...settings } })
    if (url === '/admin/saml') return Promise.resolve({ data: { ...samlConfig } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

afterEach(() => vi.restoreAllMocks())

async function openPage() {
  const user = userEvent.setup()
  const view = renderWithQuery(<AuthenticationSettingsPage />)
  await screen.findByRole('heading', { name: 'Authentication' })
  await waitFor(() => expect(screen.queryByRole('status', { name: 'Loading' })).toBeNull())
  await screen.findByRole('switch', { name: TOGGLE })
  return { user, ...view }
}

const toggle = () => screen.getByRole('switch', { name: TOGGLE })
// The page's own Save: the last one, since the OIDC section has a Save changes of its own above it.
const pageSave = () => screen.getAllByRole('button', { name: 'Save changes' }).at(-1)!

describe('SAML section', () => {
  it('shows the whole form with the switch off, certificate and key included', async () => {
    await openPage()
    expect(toggle().getAttribute('aria-checked')).toBe('false')
    expect(screen.getByRole('button', { name: 'Save SAML config' })).toBeTruthy()
    expect(screen.getByLabelText('IdP metadata URL')).toBeTruthy()
    expect(screen.getByText(/SP certificate \(PEM\)/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Upload .pem / .crt' })).toBeTruthy()
    expect(screen.getByText(/SP private key \(PEM\)/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Upload .pem / .key' })).toBeTruthy()
  })

  it('names the switch for what it does, and says what turns SAML on', async () => {
    await openPage()
    expect(screen.queryByText('Enable SAML login')).toBeNull()
    const hint = screen.getByText(/Save SAML config/, { selector: 'div' })
    expect(hint.textContent).toMatch(/administrators keep password sign-in/i)
    expect(hint.textContent).toMatch(/password sign-in for staff and users/i)
  })

  it('shows the Entity ID and the ACS URL before SAML is configured, and copies them', async () => {
    const { user } = await openPage()
    const entity = screen.getByText(ENTITY_ID)
    const acs = screen.getByText(ACS)
    expect(within(entity.closest('div')!).getByText('Entity ID')).toBeTruthy()
    expect(within(acs.closest('div')!).getByText('ACS URL')).toBeTruthy()

    await user.click(screen.getByRole('button', { name: 'Copy Entity ID' }))
    await waitFor(async () => expect(await navigator.clipboard.readText()).toBe(ENTITY_ID))
    await user.click(screen.getByRole('button', { name: 'Copy ACS URL' }))
    await waitFor(async () => expect(await navigator.clipboard.readText()).toBe(ACS))
  })

  it('keeps a switch that was set and not saved when Save SAML config is followed by a settings refetch', async () => {
    vi.spyOn(api, 'put').mockResolvedValue({ data: undefined })
    const { user, queryClient } = await openPage()

    await user.click(toggle())
    expect(toggle().getAttribute('aria-checked')).toBe('true')
    await user.type(screen.getByLabelText('IdP metadata URL'), 'https://idp.example.com/metadata')
    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))
    await waitFor(() => expect(api.put).toHaveBeenCalled())

    // The refetch the SAML save causes: the dump now carries the new URL.
    settings = { ...settings, saml_metadata_url: 'https://idp.example.com/metadata' }
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] }) })

    expect(toggle().getAttribute('aria-checked')).toBe('true')
    expect(screen.getByRole('button', { name: 'Save SAML config' })).toBeTruthy()
    expect(screen.getByLabelText('IdP metadata URL')).toBeTruthy()
  })

  it('sends saml_enabled with the page Save, and not with Save SAML config', async () => {
    const put = vi.spyOn(api, 'put').mockResolvedValue({ data: undefined })
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined })
    const { user } = await openPage()

    await user.click(toggle())
    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(Object.keys(put.mock.calls[0][1] as object)).not.toContain('saml_enabled')
    expect(patch).not.toHaveBeenCalled()

    await user.click(pageSave())
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls[0][1]).toEqual({ saml_enabled: true })
  })

  it('shows the server warning when saml_enabled was saved while SAML is not loaded', async () => {
    const warning = 'Password sign-in is limited to administrators, but SAML is not loaded: staff and users cannot sign in.'
    vi.spyOn(api, 'patch').mockResolvedValue({ data: { warning } })
    const { user } = await openPage()

    await user.click(toggle())
    await user.click(pageSave())

    const note = await screen.findByText(warning)
    expect(note.closest('[role="status"]')).not.toBeNull()
  })
})
