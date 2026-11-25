import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #445: "Guests may attach files" sits under guest submission and depends on
// it; its size slider is 1 MB up to the per-file limit (#673), 5 by default.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  useBlocker: () => ({ status: 'idle' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { AttachmentSettingsPage } from './settings/AttachmentSettingsPage'
import { GuestAccessSettingsPage } from './settings/GuestAccessSettingsPage'

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})
afterEach(() => vi.restoreAllMocks())

function stubSettings(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
}

async function waitLoaded() {
  await waitFor(() => expect(screen.queryByRole('status', { name: 'Loading' })).toBeNull())
}

async function renderSettings(settings: Record<string, unknown>) {
  const patch = stubSettings(settings)
  renderWithQuery(<GuestAccessSettingsPage />)
  await screen.findByRole('heading', { name: 'Guest Access' })
  await waitLoaded()
  return patch
}

async function renderAttachments(settings: Record<string, unknown>) {
  const patch = stubSettings(settings)
  renderWithQuery(<AttachmentSettingsPage />)
  await screen.findByRole('heading', { name: 'File Attachments' })
  await waitLoaded()
  return patch
}

const toggle = () => screen.getByRole('switch', { name: /guests may attach files/i }) as HTMLButtonElement
const slider = () => screen.getByRole('slider', { name: /largest file a guest may attach/i }) as HTMLInputElement
const overall = () => screen.getByRole('spinbutton', { name: /largest file anyone may attach/i }) as HTMLInputElement
const save = () => screen.getAllByRole('button', { name: /save changes/i }).at(-1)!
const ON = { guest_submission_enabled: true, guest_attachments_enabled: true }

describe('the guest attachments settings', () => {
  it('is off by default and the slider starts at 5 MB', async () => {
    await renderSettings({ guest_submission_enabled: true })
    expect(toggle().getAttribute('aria-checked')).toBe('false')
    expect(slider().value).toBe('5')
  })

  it('cannot be switched on while guest submission is off, and says why', async () => {
    await renderSettings({ guest_submission_enabled: false, guest_attachments_enabled: true })
    expect(toggle().disabled).toBe(true)
    expect(slider().disabled).toBe(true)
    expect(screen.getAllByText(/only takes effect while guest submission is on/i).length).toBeGreaterThan(0)
  })

  it('is enabled once guest submission is on, and the slider follows the switch', async () => {
    await renderSettings({ guest_submission_enabled: true })
    expect(toggle().disabled).toBe(false)
    expect(slider().disabled).toBe(true) // nothing to size while attaching is off
    await userEvent.click(toggle())
    expect(slider().disabled).toBe(false)
  })

  it('saves the switch and the size under their setting names', async () => {
    const patch = await renderSettings({ guest_submission_enabled: true, guest_attachment_max_mb: 5 })
    await userEvent.click(toggle())
    slider().focus()
    await userEvent.keyboard('{ArrowRight}')
    await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls.at(-1)![1]).toMatchObject({ guest_attachments_enabled: true, guest_attachment_max_mb: 6 })
  })

  describe('the per-ticket cap and the hourly rate', () => {
    const cap = () => screen.getByRole('spinbutton', { name: /most files a guest can attach to one ticket/i }) as HTMLInputElement
    const rate = () => screen.getByRole('spinbutton', { name: /uploads per guest link per hour/i }) as HTMLInputElement

    it('start at 5 and 3, with their ranges', async () => {
      await renderSettings({ guest_submission_enabled: true, guest_attachments_enabled: true })
      expect(cap().value).toBe('5')
      expect([cap().min, cap().max]).toEqual(['1', '50'])
      expect(rate().value).toBe('3')
      expect([rate().min, rate().max]).toEqual(['1', '30'])
    })

    it('are unavailable while guest uploads are off', async () => {
      await renderSettings({ guest_submission_enabled: true, guest_attachments_enabled: false })
      expect(cap().disabled).toBe(true)
      expect(rate().disabled).toBe(true)
      await userEvent.click(toggle())
      expect(cap().disabled).toBe(false)
      expect(rate().disabled).toBe(false)
    })

    it('save under their setting names, and stay inside their ranges', async () => {
      const patch = await renderSettings({ guest_submission_enabled: true, guest_attachments_enabled: true })
      await userEvent.clear(cap())
      await userEvent.type(cap(), '12')
      await userEvent.clear(rate())
      await userEvent.type(rate(), '99') // above 30: held at the top of the range
      expect(rate().getAttribute('aria-invalid')).toBe('true') // 99 is typed, 30 is what is kept
      expect(cap().getAttribute('aria-invalid')).not.toBe('true')
      await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)
      await waitFor(() => expect(patch).toHaveBeenCalled())
      expect(patch.mock.calls.at(-1)![1]).toMatchObject({
        guest_attachment_max_per_ticket: 12,
        guest_attachment_uploads_per_hour: 30,
      })
    })
  })
})

// #673: the guest slider stops at the per-file limit. The slider's top is the
// stored per-file limit (Guest Access cannot edit it).
describe('the guest size slider and the per-file limit', () => {
  it('tops out at the stored per-file limit, or 25 when none is stored, with the two ends as ticks', async () => {
    await renderSettings({ ...ON, attachment_max_mb: 40 })
    expect(slider().max).toBe('40')
    expect(Array.from(screen.getByTestId('guest-size-ticks').children).map((c) => c.textContent)).toEqual(['1 MB', '40 MB'])
  })

  it('tops out at 25 when no limit is stored', async () => {
    await renderSettings(ON)
    expect(slider().max).toBe('25')
  })

  it('says what the description above it holds, without a literal 25', async () => {
    await renderSettings({ ...ON, attachment_max_mb: 10 })
    expect(screen.getByText(
      'The largest file a guest may attach. It can never exceed the per-file limit for every upload, 10 MB, set under System → File Attachments.',
    )).toBeTruthy()
  })

  it('sits a stored 20 at 10 under a limit of 10, with the saved-as note', async () => {
    await renderSettings({ ...ON, attachment_max_mb: 10, guest_attachment_max_mb: 20 })
    expect(slider().value).toBe('10')
    expect(screen.getByRole('note').textContent).toMatch(/^Saved as 20 MB\. Every upload is limited to 10 MB/)
  })

  it('sends attachment_max_mb only when it changed', async () => {
    const patch = await renderSettings({ ...ON, attachment_max_mb: 25, guest_attachment_max_mb: 5 })
    await userEvent.click(toggle())
    await userEvent.click(save())
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls.at(-1)![1]).not.toHaveProperty('attachment_max_mb')
  })
})

describe('the File size section on the File Attachments page', () => {
  it('follows Uploads, and its guest line reads the stored guest value', async () => {
    await renderAttachments({ attachment_max_mb: 10, guest_attachment_max_mb: 20 })
    const headings = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)
    expect(headings.indexOf('File size')).toBe(headings.indexOf('Uploads') + 1)
    expect(overall().value).toBe('10')
    expect(screen.getByTestId('guest-limit-line').textContent).toBe(
      'Guests: up to 10 MB. Their own limit is 20 MB; this one is lower, so it applies.',
    )
  })

  it('shows 25 and the 5 MB guest default when nothing is stored', async () => {
    await renderAttachments({})
    expect(overall().value).toBe('25')
    expect(screen.getByTestId('guest-limit-line').textContent).toBe('Guests: up to 5 MB.')
  })

  it.each([
    ['required', true],
    ['permissive', true],
    ['off', false],
  ])('shows the scanner hint above 25 only while scanning (%s)', async (policy, shown) => {
    await renderAttachments({ attachment_max_mb: 50, attachment_scan_policy: policy })
    expect(!!screen.queryByText(/your virus scanner must accept files this large/i)).toBe(shown)
  })

  it('saves the limit under its setting name', async () => {
    const patch = await renderAttachments({ attachment_max_mb: 25 })
    await userEvent.clear(overall())
    await userEvent.type(overall(), '60')
    await userEvent.click(save())
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls.at(-1)![1]).toEqual({ attachment_max_mb: 60 })
  })
})

// #674: each settings page has its own draft. Nothing typed on File Attachments
// reaches Guest Access until it is saved; what the two pages share is the stored
// settings, which a save refetches.
describe('the guest size slider and the per-file limit across the two pages', () => {
  it('follows a new per-file limit once File Attachments has saved it, not before', async () => {
    // A settings endpoint that keeps what a save wrote, as the server does.
    const stored: Record<string, unknown> = { ...ON, attachment_max_mb: 10, guest_attachment_max_mb: 5 }
    vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/settings') return Promise.resolve({ data: { ...stored } })
      if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
      if (url === '/site') return Promise.resolve({ data: {} })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    const patch = vi.spyOn(api, 'patch').mockImplementation(((_url: string, body: Record<string, unknown>) => {
      Object.assign(stored, body)
      return Promise.resolve({ data: undefined })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)

    // Both pages on screen under one query client, as one browser session holds them.
    renderWithQuery(
      <>
        <div data-testid="attachments"><AttachmentSettingsPage /></div>
        <div data-testid="guest"><GuestAccessSettingsPage /></div>
      </>,
    )
    const attachments = within(await screen.findByTestId('attachments'))
    const guest = within(screen.getByTestId('guest'))
    await waitFor(() => expect(screen.queryAllByRole('status', { name: 'Loading' })).toHaveLength(0))
    const guestSlider = () => guest.getByRole('slider', { name: /largest file a guest may attach/i }) as HTMLInputElement
    expect(guestSlider().max).toBe('10')

    const limit = attachments.getByRole('spinbutton', { name: /largest file anyone may attach/i })
    await userEvent.clear(limit)
    await userEvent.type(limit, '40')
    expect(guestSlider().max).toBe('10') // typed, not saved: another page's draft is not shared

    await userEvent.click(attachments.getAllByRole('button', { name: /save changes/i }).at(-1)!)
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls.at(-1)![1]).toEqual({ attachment_max_mb: 40 })
    await waitFor(() => expect(guestSlider().max).toBe('40'))
  })

  it('does not send a stored guest value the administrator never touched', async () => {
    const patch = await renderAttachments({ ...ON, attachment_max_mb: 25, guest_attachment_max_mb: 20 })
    await userEvent.clear(overall())
    await userEvent.type(overall(), '10')
    await userEvent.click(save())
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls.at(-1)![1]).toEqual({ attachment_max_mb: 10 })
  })

  describe('a guest value above the per-file limit', () => {
    it('is shown at the limit with no saved-as note, and saved at the limit', async () => {
      const patch = await renderSettings({ ...ON, attachment_max_mb: 10, guest_attachment_max_mb: 5 })
      fireEvent.change(slider(), { target: { value: '20' } })
      expect(slider().value).toBe('10')
      expect(screen.queryByRole('note')).toBeNull()
      await userEvent.click(save())
      await waitFor(() => expect(patch).toHaveBeenCalled())
      expect(patch.mock.calls.at(-1)![1]).toEqual({ guest_attachment_max_mb: 10 })
    })

    it('is saved at the limit when a stored value above it is changed by the same save', async () => {
      const patch = await renderSettings({ ...ON, attachment_max_mb: 10, guest_attachment_max_mb: 20 })
      expect(slider().value).toBe('10')
      slider().focus()
      await userEvent.keyboard('{ArrowLeft}')
      await userEvent.click(save())
      await waitFor(() => expect(patch).toHaveBeenCalled())
      expect(patch.mock.calls.at(-1)![1]).toEqual({ guest_attachment_max_mb: 9 })
    })
  })
})
