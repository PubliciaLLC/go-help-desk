import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #461: the IdP's metadata is given as a URL or as XML, pasted or uploaded into
// one text box. The XML is shown again after saving (it is public data); the
// private key never is. The server is the only validator, so the page does no
// size or format check of its own.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { SettingsPage } from './SettingsPage'

const XML = '<?xml version="1.0"?>\n<EntityDescriptor entityID="https://idp.example.com"/>'
const XML2 = '<EntityDescriptor entityID="https://other.example.com"/>'
const CERT = '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----'

let samlConfig: Record<string, unknown>

beforeEach(() => {
  samlConfig = {
    configured: true,
    metadata_url: '',
    metadata_xml: XML,
    cert_pem: CERT,
    sp_metadata_url: 'https://hd.example.com/api/v1/auth/saml/metadata',
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
    if (url === '/admin/settings') return Promise.resolve({ data: { saml_enabled: true } })
    if (url === '/admin/saml') return Promise.resolve({ data: samlConfig })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

afterEach(() => vi.restoreAllMocks())

async function openSAML() {
  const user = userEvent.setup()
  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await user.click(screen.getByRole('button', { name: 'Authentication' }))
  await screen.findByRole('button', { name: 'Save SAML config' })
  return user
}

describe('SAML IdP metadata', () => {
  it('shows saved XML again, with the XML source chosen', async () => {
    await openSAML()
    await waitFor(() => expect((screen.getByRole('radio', { name: 'XML' }) as HTMLInputElement).checked).toBe(true))
    expect((screen.getByLabelText('IdP metadata XML') as HTMLTextAreaElement).value).toBe(XML)
  })

  it('selects the URL source when a URL is saved', async () => {
    samlConfig = { ...samlConfig, metadata_url: 'https://idp/meta', metadata_xml: '' }
    await openSAML()
    await waitFor(() => expect((screen.getByRole('radio', { name: 'URL' }) as HTMLInputElement).checked).toBe(true))
    expect((screen.getByLabelText('IdP metadata URL') as HTMLInputElement).value).toBe('https://idp/meta')
  })

  it('fills the text box from an uploaded file', async () => {
    const user = await openSAML()
    await waitFor(() => expect((screen.getByRole('radio', { name: 'XML' }) as HTMLInputElement).checked).toBe(true))

    await user.upload(
      screen.getByLabelText('IdP metadata XML file'),
      new File([XML2], 'metadata.xml', { type: 'text/xml' }),
    )
    await waitFor(() => expect((screen.getByLabelText('IdP metadata XML') as HTMLTextAreaElement).value).toBe(XML2))
  })

  it('sends the XML and clears the URL when saving in XML mode, without a key', async () => {
    const put = vi.spyOn(api, 'put').mockResolvedValue({ data: undefined })
    const user = await openSAML()
    await waitFor(() => expect((screen.getByLabelText('IdP metadata XML') as HTMLTextAreaElement).value).toBe(XML))

    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(put).toHaveBeenCalledWith('/admin/saml', { metadata_url: '', metadata_xml: XML, cert_pem: CERT })
    // The key is write-only: a save that did not touch it must leave it out.
    expect(Object.keys(put.mock.calls[0][1] as object)).not.toContain('key_pem')
  })

  it('sends the URL and clears the XML when saving in URL mode', async () => {
    const put = vi.spyOn(api, 'put').mockResolvedValue({ data: undefined })
    const user = await openSAML()
    await waitFor(() => expect((screen.getByRole('radio', { name: 'XML' }) as HTMLInputElement).checked).toBe(true))

    await user.click(screen.getByRole('radio', { name: 'URL' }))
    await user.type(screen.getByLabelText('IdP metadata URL'), 'https://idp.example.com/metadata')
    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(put).toHaveBeenCalledWith('/admin/saml', {
      metadata_url: 'https://idp.example.com/metadata',
      metadata_xml: '',
      cert_pem: CERT,
    })
  })

  it("shows the server's message when it refuses the metadata", async () => {
    vi.spyOn(api, 'put').mockRejectedValue({
      isAxiosError: true,
      message: 'Request failed with status code 400',
      response: { data: { error: { code: 'invalid_saml_metadata', message: 'not usable SAML IdP metadata: EOF' } } },
    })
    const user = await openSAML()
    await waitFor(() => expect((screen.getByLabelText('IdP metadata XML') as HTMLTextAreaElement).value).toBe(XML))

    await user.click(screen.getByRole('button', { name: 'Save SAML config' }))
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('not usable SAML IdP metadata: EOF')
  })
})
