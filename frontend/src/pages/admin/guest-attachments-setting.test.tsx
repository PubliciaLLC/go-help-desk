import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
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
// Only the cross-page block at the end still renders the tabbed page. See there.
import { SettingsPage } from './SettingsPage'

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
    expect(screen.getByText(/only takes effect while guest submission is on/i)).toBeTruthy()
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
      'The largest file a guest may attach. It can never exceed the per-file limit for every upload, 10 MB, set under Attachments.',
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

// NOT MOVED in task 6. These tests need an unsaved edit on Guest Access and an
// unsaved edit on File Attachments to be visible to each other, and to be saved
// by one Save. That is the tabbed SettingsPage's single shared draft. DESIGN.md
// (#674) gives each page its own draft and makes the Guest slider's top the
// stored per-file limit, so these assertions describe behaviour the design
// removes. They keep rendering SettingsPage until the owner decides how they
// are rewritten or dropped.
describe('the guest size slider and the per-file limit on the tabbed page (not moved)', () => {
  const tab = (name: string) => screen.getByRole('button', { name })

  async function renderTabbed(settings: Record<string, unknown>) {
    const patch = stubSettings(settings)
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })
    return patch
  }

  async function typeOverall(n: string) {
    await userEvent.click(tab('Attachments'))
    await userEvent.clear(overall())
    await userEvent.type(overall(), n)
    await userEvent.click(tab('General'))
  }

  it('raises the slider top in the same render when the per-file limit is typed, before saving', async () => {
    await renderTabbed({ ...ON, attachment_max_mb: 10 })
    expect(slider().max).toBe('10')
    await typeOverall('40')
    expect(slider().max).toBe('40')
  })

  it('does not send a stored guest value the administrator never touched', async () => {
    const patch = await renderTabbed({ ...ON, attachment_max_mb: 25, guest_attachment_max_mb: 20 })
    await typeOverall('10')
    expect(slider().value).toBe('10')
    await userEvent.click(save())
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls.at(-1)![1]).toEqual({ attachment_max_mb: 10 })
  })

  describe('an unsaved guest value above an unsaved per-file limit', () => {
    async function draggedTo20() {
      const patch = await renderTabbed({ ...ON, attachment_max_mb: 25, guest_attachment_max_mb: 5 })
      fireEvent.change(slider(), { target: { value: '20' } })
      expect(slider().value).toBe('20')
      return patch
    }

    it('is shown at the limit with no saved-as note, and saved at the limit', async () => {
      const patch = await draggedTo20()
      await typeOverall('10')
      expect(slider().value).toBe('10')
      expect(screen.queryByRole('note')).toBeNull()
      await userEvent.click(save())
      await waitFor(() => expect(patch).toHaveBeenCalled())
      expect(patch.mock.calls.at(-1)![1]).toEqual({ attachment_max_mb: 10, guest_attachment_max_mb: 10 })
    })

    it('shows the dragged value again when the limit is raised before saving', async () => {
      await draggedTo20()
      await typeOverall('10')
      expect(slider().value).toBe('10')
      await typeOverall('30')
      expect(slider().value).toBe('20')
    })

    it('reads the same on the Attachments tab: the guest figure the page shows, not the raw draft', async () => {
      await draggedTo20()
      await userEvent.click(tab('Attachments'))
      await userEvent.clear(overall())
      await userEvent.type(overall(), '10')
      expect(screen.getByTestId('guest-limit-line').textContent).toBe('Guests: up to 10 MB.')
    })
  })
})
