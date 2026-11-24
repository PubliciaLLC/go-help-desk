import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
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
const CERT = '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n'
const KEY = '-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----'

let settings: Record<string, unknown>
let samlConfig: Record<string, unknown>

beforeEach(() => {
  settings = { saml_enabled: false, saml_metadata_url: '', password_rehash_grace_days: 30 }
  samlConfig = {
    configured: false,
    live: false,
    metadata_url: '',
    metadata_xml: '',
    cert_pem: '',
    cert_not_after: null,
    cert_generated: false,
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
  it('shows the whole form with the switch off, and no certificate to upload until asked', async () => {
    await openPage()
    expect(toggle().getAttribute('aria-checked')).toBe('false')
    expect(screen.getByRole('button', { name: 'Save SAML config' })).toBeTruthy()
    expect(screen.getByLabelText('IdP metadata URL')).toBeTruthy()
    expect(screen.getByText(/created automatically when you save/i)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Upload .pem / .crt' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Upload .pem / .key' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Regenerate' })).toBeNull()
    expect(screen.queryByRole('link', { name: 'Download certificate' })).toBeNull()
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

  it('says Copied in a status line, and says so when the clipboard is refused', async () => {
    const { user } = await openPage()
    const entity = screen.getByText(ENTITY_ID).parentElement!

    await user.click(screen.getByRole('button', { name: 'Copy Entity ID' }))
    expect((await within(entity).findByRole('status')).textContent).toBe('Copied')

    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('denied'))
    await user.click(screen.getByRole('button', { name: 'Copy Entity ID' }))
    await waitFor(() => expect(within(entity).getByRole('status').textContent).toBe('Copy failed, select the text'))
  })

  it('keeps a switch that was set and not saved when Save SAML config is followed by a settings refetch', async () => {
    // The save changes what the server holds: the new URL, and (so the test can
    // tell the refetch has reached the page) a second, visible setting.
    vi.spyOn(api, 'put').mockImplementation((() => {
      settings = { ...settings, saml_metadata_url: 'https://idp.example.com/metadata', password_rehash_grace_days: 45 }
      return Promise.resolve({ data: undefined })
    }) as never)
    const { user } = await openPage()
    const days = () => screen.getByLabelText('Days before an old password must be reset') as HTMLInputElement
    expect(days().value).toBe('30')

    await user.click(toggle())
    expect(toggle().getAttribute('aria-checked')).toBe('true')
    await user.type(screen.getByLabelText('IdP metadata URL'), 'https://idp.example.com/metadata')
    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))

    // The SAML save refetches the settings by itself, and the page has them.
    await waitFor(() => expect(days().value).toBe('45'))
    expect(toggle().getAttribute('aria-checked')).toBe('true')
    expect(screen.getByRole('button', { name: 'Save SAML config' })).toBeTruthy()
    expect(screen.getByLabelText('IdP metadata URL')).toBeTruthy()
  })

  it('does not send the private key again after it has been saved', async () => {
    const put = vi.spyOn(api, 'put').mockResolvedValue({ data: undefined })
    const { user, container } = await openPage()

    await user.click(screen.getByRole('button', { name: 'Replace…' }))
    await user.upload(
      container.querySelector('input[type="file"][accept=".pem,.crt,.cer"]') as HTMLInputElement,
      new File([CERT], 'sp.crt', { type: 'application/x-pem-file' }),
    )
    await user.upload(
      container.querySelector('input[type="file"][accept=".pem,.key"]') as HTMLInputElement,
      new File([KEY], 'sp.key', { type: 'application/x-pem-file' }),
    )
    await waitFor(() => expect(container.querySelectorAll('textarea').length).toBeGreaterThan(0))
    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put.mock.calls[0][1]).toMatchObject({ cert_pem: CERT, key_pem: KEY })

    // The key is not kept in the page: no box shows it, the upload controls are
    // put away, and the next save leaves both halves out.
    await waitFor(() => expect(container.querySelector('textarea')).toBeNull())
    expect(screen.queryByRole('button', { name: 'Upload .pem / .key' })).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
    expect(Object.keys(put.mock.calls[1][1] as object)).not.toContain('key_pem')
    expect(Object.keys(put.mock.calls[1][1] as object)).not.toContain('cert_pem')
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

// The service-provider certificate: the server makes it when the IdP metadata is
// first saved. The page shows it, offers its public half for download, and keeps
// the upload controls and the regeneration out of the way until asked for.
describe('SAML service-provider certificate', () => {
  const generated = () => {
    samlConfig = {
      ...samlConfig,
      configured: true,
      cert_pem: CERT,
      cert_not_after: '2036-11-24T12:00:00Z',
      cert_generated: true,
    }
  }

  it('says a generated certificate is generated automatically, with its expiry and a download', async () => {
    generated()
    const { container } = await openPage()
    await screen.findByText('Generated automatically')
    expect(screen.getByText(/Expires/).textContent).toMatch(/2036/)

    const download = screen.getByRole('link', { name: 'Download certificate' }) as HTMLAnchorElement
    expect(download.getAttribute('download')).toBe('saml-sp-certificate.pem')
    expect(decodeURIComponent(download.getAttribute('href')!.split(',')[1])).toBe(CERT)

    // Nothing to upload, and the key is not mentioned beyond where it is kept.
    expect(screen.queryByRole('button', { name: 'Upload .pem / .crt' })).toBeNull()
    expect(container.textContent).not.toMatch(/PRIVATE KEY/)
    expect(container.querySelector('textarea')).toBeNull()
  })

  it('calls an uploaded certificate uploaded, with the same expiry and download', async () => {
    generated()
    samlConfig.cert_generated = false
    await openPage()
    await screen.findByText('Uploaded certificate')
    expect(screen.queryByText('Generated automatically')).toBeNull()
    expect(screen.getByText(/Expires/).textContent).toMatch(/2036/)
    expect(screen.getByRole('link', { name: 'Download certificate' })).toBeTruthy()
  })

  it('shows the upload controls only after Replace…, and puts them away again on Cancel', async () => {
    generated()
    const { user } = await openPage()
    await screen.findByText('Generated automatically')

    await user.click(screen.getByRole('button', { name: 'Replace…' }))
    expect(screen.getByRole('button', { name: 'Upload .pem / .crt' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Upload .pem / .key' })).toBeTruthy()

    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('button', { name: 'Upload .pem / .crt' })).toBeNull()
  })

  it('saves without the certificate or key when nothing was replaced', async () => {
    generated()
    const put = vi.spyOn(api, 'put').mockResolvedValue({ data: undefined })
    const { user } = await openPage()
    await screen.findByText('Generated automatically')

    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(Object.keys(put.mock.calls[0][1] as object).sort()).toEqual(['metadata_url', 'metadata_xml'])
  })

  it('asks before regenerating, and sends nothing when cancelled', async () => {
    generated()
    const put = vi.spyOn(api, 'put').mockResolvedValue({ data: undefined })
    const { user } = await openPage()
    await screen.findByText('Generated automatically')

    await user.click(screen.getByRole('button', { name: 'Regenerate' }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog.textContent).toMatch(/identity provider/i)
    expect(dialog.textContent).toMatch(/new certificate/i)

    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(put).not.toHaveBeenCalled()
  })

  it('regenerates on confirm: sends only regenerate_cert, then loads the new certificate', async () => {
    generated()
    const NEW = '-----BEGIN CERTIFICATE-----\nNEWNEW\n-----END CERTIFICATE-----\n'
    const put = vi.spyOn(api, 'put').mockImplementation((() => {
      samlConfig = { ...samlConfig, cert_pem: NEW, cert_not_after: '2036-12-01T12:00:00Z' }
      return Promise.resolve({ data: undefined })
    }) as never)
    const { user } = await openPage()
    await screen.findByText('Generated automatically')

    await user.click(screen.getByRole('button', { name: 'Regenerate' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Regenerate' }))

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put.mock.calls[0][1]).toEqual({ regenerate_cert: true })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    // The page carries the new certificate, not the old one.
    await waitFor(() => {
      const href = (screen.getByRole('link', { name: 'Download certificate' }) as HTMLAnchorElement).getAttribute('href')!
      expect(decodeURIComponent(href.split(',')[1])).toBe(NEW)
    })
    expect(screen.getByText(/new certificate generated/i)).toBeTruthy()
  })

  it("shows the server's message in the dialog when it refuses, and keeps it open", async () => {
    generated()
    vi.spyOn(api, 'put').mockRejectedValue({
      isAxiosError: true,
      message: 'Request failed with status code 500',
      response: { data: { error: { code: 'internal_error', message: 'no luck' } } },
    })
    const { user } = await openPage()
    await screen.findByText('Generated automatically')

    await user.click(screen.getByRole('button', { name: 'Regenerate' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Regenerate' }))

    expect((await within(dialog).findByRole('alert')).textContent).toContain('no luck')
    expect(screen.getByRole('dialog')).toBeTruthy()
  })
})
