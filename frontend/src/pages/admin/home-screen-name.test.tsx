import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosResponse } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The home screen name of System -> Branding (#708): a short name for the label
// under the icon on a phone, used only when the administrator asks for one.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  useBlocker: () => ({ status: 'idle' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { BrandingSettingsPage } from './settings/BrandingSettingsPage'

let dump: Record<string, unknown>
let patchSpy: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  dump = { site_name: 'Acme IT' }
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
    if (url === '/admin/settings') return Promise.resolve({ data: { ...dump } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    if (url === '/admin/settings/icon') {
      return Promise.resolve({
        data: { set: false, background: '#ffffff', version: '', source_size: 0, updated_at: null, files: [] },
      })
    }
    return Promise.resolve({ data: [] })
  }) as never)
  patchSpy = vi.spyOn(api, 'patch').mockImplementation(((_url: string, body: Record<string, unknown>) => {
    dump = { ...dump, ...body }
    return Promise.resolve({ data: undefined })
  }) as never)
})
afterEach(() => vi.restoreAllMocks())

async function renderBranding() {
  renderWithQuery(<BrandingSettingsPage />)
  await screen.findByRole('heading', { level: 1, name: 'Branding' })
  return (await screen.findByRole('checkbox', { name: 'Use a different name on the home screen' })) as HTMLInputElement
}

const field = () => screen.queryByLabelText('Home screen name') as HTMLInputElement | null
const note = () => screen.queryByText('Most home screens show about 12 characters and cut longer names short.')
const save = () => screen.getByRole('button', { name: /save changes/i }) as HTMLButtonElement

describe('the home screen name', () => {
  it('is off when no short name is stored: no field, and a line saying what happens', async () => {
    const box = await renderBranding()
    expect(box.checked).toBe(false)
    expect(field()).toBeNull()
    expect(screen.getByText('The home screen uses the site name.')).toBeTruthy()
  })

  it('the site name row says it is also the install and home screen name by default', async () => {
    await renderBranding()
    expect(
      screen.getByText(
        'Displayed in the sidebar header and browser tab when no logo is set. Also the name used when someone installs the site or adds it to a home screen, unless you set a different home screen name below.',
      ),
    ).toBeTruthy()
  })

  it('ticking shows the field, placeholder the site name, 30 characters allowed', async () => {
    const box = await renderBranding()
    await userEvent.click(box)
    expect(field()).toBeTruthy()
    expect(field()!.placeholder).toBe('Acme IT')
    // No maxLength: it counts UTF-16 units, the server and the counter count characters.
    expect(field()!.hasAttribute('maxlength')).toBe(false)
    expect(screen.queryByText('The home screen uses the site name.')).toBeNull()
    expect(screen.getByText("Shown under the icon when someone adds this site to a phone's home screen.")).toBeTruthy()
    expect(screen.getByText('0/30')).toBeTruthy()
  })

  it('falls back to the default name as placeholder when the site name is unset', async () => {
    dump = {}
    const box = await renderBranding()
    await userEvent.click(box)
    expect(field()!.placeholder).toBe('Go Help Desk')
  })

  it('saves only the short name', async () => {
    const box = await renderBranding()
    await userEvent.click(box)
    await userEvent.type(field()!, 'Help')
    expect(screen.getByText('4/30')).toBeTruthy()
    await userEvent.click(save())
    await waitFor(() => expect(patchSpy).toHaveBeenCalledTimes(1))
    expect(patchSpy).toHaveBeenCalledWith('/admin/settings', { site_short_name: 'Help' })
  })

  it('is ticked and filled when a short name is stored; unticking and saving clears it', async () => {
    dump = { site_name: 'Acme IT', site_short_name: 'IT' }
    const box = await renderBranding()
    expect(box.checked).toBe(true)
    expect(field()!.value).toBe('IT')

    await userEvent.click(box)
    expect(field()).toBeNull()
    expect(screen.getByText('The home screen uses the site name.')).toBeTruthy()
    await userEvent.click(save())
    await waitFor(() => expect(patchSpy).toHaveBeenCalledTimes(1))
    expect(patchSpy).toHaveBeenCalledWith('/admin/settings', { site_short_name: '' })
  })

  it('warns, without blocking, above 12 characters and not at 12', async () => {
    const box = await renderBranding()
    await userEvent.click(box)
    fireEvent.change(field()!, { target: { value: '123456789012' } })
    expect(note()).toBeNull()
    fireEvent.change(field()!, { target: { value: '1234567890123' } })
    expect(note()).toBeTruthy()
    expect(note()!.getAttribute('role')).toBe('note')
    expect(screen.getByText('13/30')).toBeTruthy()
    // The counter and the note describe the field to a screen reader.
    const ids = (field()!.getAttribute('aria-describedby') ?? '').split(' ')
    expect(ids).toContain(screen.getByText('13/30').id)
    expect(ids).toContain(note()!.id)
    // Not a blocker: the save button is still enabled.
    expect(save().disabled).toBe(false)
  })

  it('measures the trimmed text, not the spaces around it, in the counter and in the note', async () => {
    const box = await renderBranding()
    await userEvent.click(box)
    fireEvent.change(field()!, { target: { value: '   Help       ' } })
    expect(note()).toBeNull()
    expect(screen.getByText('4/30')).toBeTruthy()
    fireEvent.change(field()!, { target: { value: '  1234567890123  ' } })
    expect(screen.getByText('13/30')).toBeTruthy()
    expect(note()).toBeTruthy()
  })

  it('counts characters, not UTF-16 units: 30 emoji fit, 31 are over', async () => {
    const box = await renderBranding()
    await userEvent.click(box)
    fireEvent.change(field()!, { target: { value: '😀'.repeat(30) } })
    expect(field()!.value).toBe('😀'.repeat(30))
    const counter = screen.getByText('30/30')
    expect(counter.className).not.toContain('text-red')
    expect(field()!.getAttribute('aria-invalid')).not.toBe('true')
  })

  it('does not cut typing off at 30: the counter turns red above it and the server has the last word', async () => {
    const box = await renderBranding()
    await userEvent.click(box)
    fireEvent.change(field()!, { target: { value: 'a'.repeat(31) } })
    expect(field()!.value).toHaveLength(31)
    const counter = screen.getByText('31/30')
    expect(counter.className).toContain('text-red')
    expect(field()!.getAttribute('aria-invalid')).toBe('true')
    expect(save().disabled).toBe(false)
  })

  it('shows the server refusal through the save bar', async () => {
    const box = await renderBranding()
    await userEvent.click(box)
    await userEvent.type(field()!, 'Help')
    const message = 'the home screen name can be at most 30 characters and cannot contain control characters'
    const err = new AxiosError('Request failed with status code 400', '400')
    err.response = {
      status: 400,
      statusText: '',
      headers: {},
      config: {},
      data: { error: { code: 'invalid_site_short_name', message } },
    } as unknown as AxiosResponse
    patchSpy.mockRejectedValueOnce(err)
    await userEvent.click(save())
    expect((await screen.findByRole('alert')).textContent).toBe(message)
  })
})
