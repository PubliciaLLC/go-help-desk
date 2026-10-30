import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The logo uploader, after SVG was removed from the server.
//
// handler_logo.go's detectLogoType now answers "unsupported file type: must be
// PNG, JPG, or GIF" for anything else, and handleServeLogo serves logo.png and
// nothing else — "An SVG left by an older release is deliberately not served…
// The instance falls back to no logo, and the settings page says why."
//
// The settings page did not say why, and still offered SVG in the picker and
// in its copy, so an administrator chose an SVG from a control that advertised
// it and received a 400. Two failures, one on each side of the upload.

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

const LOGO_URL = '/api/v1/site/logo'

beforeEach(() => {
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
})

function mockApi(site: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: {} })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: site })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

async function renderBranding(site: Record<string, unknown> = {}) {
  mockApi(site)
  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await userEvent.click(screen.getByRole('button', { name: 'Branding' }))
  await screen.findByText(/target size/i)
}

function bodyText(): string {
  return document.body.textContent ?? ''
}

function filePicker(): HTMLInputElement {
  const input = document.querySelector('input[type="file"]')
  expect(input, 'the branding panel has no file picker').toBeTruthy()
  return input as HTMLInputElement
}

describe('the logo uploader', () => {
  it('does not offer SVG in the file picker', async () => {
    await renderBranding()
    expect(filePicker().getAttribute('accept') ?? '').not.toMatch(/svg/i)
  })

  // GIF stays: the server accepts it, and dropping it would be a feature
  // removal nobody asked for.
  it('still offers the three formats the server accepts', async () => {
    await renderBranding()
    const accept = filePicker().getAttribute('accept') ?? ''
    for (const ext of ['.png', '.jpg', '.jpeg', '.gif']) {
      expect(accept, `${ext} is accepted by the server`).toContain(ext)
    }
  })

  it('does not advertise SVG, and says briefly why it is gone', async () => {
    await renderBranding()
    expect(bodyText()).toContain('PNG, JPG, or GIF')
    expect(bodyText()).not.toContain('PNG, SVG')
    expect(bodyText()).toMatch(/SVG is not accepted/i)
  })

  // An instance upgrading from a release that took SVG has one on disk, the
  // logo route now answers 404, and the sidebar quietly falls back to the site
  // name. Nothing anywhere says why the logo disappeared.
  it('explains a stored logo that no longer loads, and asks for a raster one', async () => {
    await renderBranding({ logo_url: LOGO_URL })

    const img = await screen.findByAltText(/current logo/i)
    fireEvent.error(img)

    expect(bodyText()).toMatch(/no longer served/i)
    expect(bodyText()).toMatch(/upload a png/i)
  })

  it('says nothing of the sort while the logo loads normally', async () => {
    await renderBranding({ logo_url: LOGO_URL })
    await screen.findByAltText(/current logo/i)
    expect(bodyText()).not.toMatch(/no longer served/i)
  })
})
