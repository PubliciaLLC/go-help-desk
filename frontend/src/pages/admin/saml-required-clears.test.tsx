import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The red "SAML is required but is not working" notice must go away when the
// administrator does what it says, with no refocus, no Details click and no
// reload: the security warnings are stale for five minutes, so each save has
// to ask for them again. Both ways out are pinned here: a working SAML
// configuration saved with "Save SAML config", and the switch turned off with
// the page's own Save. The page is rendered in the app shell, whose bar is the
// real one.

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

const TITLE = 'Staff and users cannot sign in: SAML is required but is not working'
const TOGGLE = 'Require SAML for staff and users'
const PROBLEM = { saml_required_not_loaded: { reason: 'not_loaded' } }

let settings: Record<string, unknown>
// What the server answers for the security warnings right now.
let warnings: Record<string, unknown>

beforeEach(() => {
  settings = { saml_enabled: true, saml_metadata_url: 'https://idp.example.com/metadata', password_rehash_grace_days: 30 }
  warnings = { insecure_secrets: [], ...PROBLEM }
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
    if (url === '/admin/saml')
      return Promise.resolve({
        data: { configured: true, live: false, metadata_url: '', metadata_xml: '', cert_pem: '', sp_metadata_url: '', sp_acs_url: '' },
      })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { ...warnings } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

afterEach(() => vi.restoreAllMocks())

async function openPage() {
  const user = userEvent.setup()
  // The page renders inside the app shell, which carries the server problems bar.
  renderWithQuery(<AuthenticationSettingsPage />)
  const bar = await screen.findByRole('region', { name: 'Server notices' })
  expect(bar.textContent).toContain(TITLE)
  await screen.findByRole('switch', { name: TOGGLE })
  await waitFor(() => expect(screen.queryByRole('status', { name: 'Loading' })).toBeNull())
  return user
}

const noBar = () => waitFor(() => expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull())

describe('the SAML required notice clears when the fix is saved', () => {
  it('after "Save SAML config" loads SAML', async () => {
    vi.spyOn(api, 'put').mockImplementation((() => {
      warnings = { insecure_secrets: [] }
      return Promise.resolve({ data: undefined })
    }) as never)
    const user = await openPage()

    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))
    await noBar()
  })

  it('after the page Save turns the switch off', async () => {
    vi.spyOn(api, 'patch').mockImplementation(((_url: string, body: Record<string, unknown>) => {
      settings = { ...settings, ...body }
      if (body.saml_enabled === false) warnings = { insecure_secrets: [] }
      return Promise.resolve({ data: undefined })
    }) as never)
    const user = await openPage()

    await user.click(screen.getByRole('switch', { name: TOGGLE }))
    await user.click(screen.getAllByRole('button', { name: 'Save changes' }).at(-1)!)
    await noBar()
  })
})
