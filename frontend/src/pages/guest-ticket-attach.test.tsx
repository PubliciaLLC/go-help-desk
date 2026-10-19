import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// #445: the guest ticket page offers "Add files" only when the instance lets
// guests attach, and uploads through the guest route with the link's token.

const { getGuestTicket, uploadGuestAttachment, getSiteConfig } = vi.hoisted(() => ({
  getGuestTicket: vi.fn(),
  uploadGuestAttachment: vi.fn(),
  getSiteConfig: vi.fn(),
}))
vi.mock('@/api/guest', async (orig) => ({
  ...(await orig<typeof import('@/api/guest')>()),
  getGuestTicket,
  uploadGuestAttachment,
}))
vi.mock('@/api/admin', async (orig) => ({
  ...(await orig<typeof import('@/api/admin')>()),
  getSiteConfig,
}))

import { GuestTicketViewPage } from './GuestTicketViewPage'
import { GuestUploadRefused } from '@/api/guest'

function ticket(status = 'In Progress') {
  return {
    tracking_number: 'GHD-2026-000042', subject: 'Printer jammed', description: 'again', status,
    created_at: '2026-09-20T10:00:00Z', replies: [],
  }
}
const addFiles = () => screen.queryByRole('button', { name: 'Add files' })

beforeEach(() => {
  getGuestTicket.mockReset().mockResolvedValue(ticket())
  uploadGuestAttachment.mockReset()
  getSiteConfig.mockReset()
  window.location.hash = '#some-token'
})

describe('attaching files from the guest page', () => {
  it('shows no control when the instance does not allow it', async () => {
    getSiteConfig.mockResolvedValue({ name: 'x', logo_url: '', version: '1', guest_submission_enabled: true, guest_attachments_enabled: false })
    renderWithQuery(<GuestTicketViewPage />)
    await screen.findByRole('textbox', { name: 'Add a reply' })
    await waitFor(() => expect(getSiteConfig).toHaveBeenCalled())
    expect(addFiles()).toBeNull()
  })

  it('shows the control, naming the limit, when it is allowed', async () => {
    getSiteConfig.mockResolvedValue({ name: 'x', logo_url: '', version: '1', guest_submission_enabled: true, guest_attachments_enabled: true, guest_attachment_max_bytes: 5 * 1024 * 1024 })
    renderWithQuery(<GuestTicketViewPage />)
    expect(await screen.findByRole('button', { name: 'Add files' })).toBeTruthy()
    expect(document.body.textContent).toMatch(/max 5\s*MB/)
  })

  it('offers none on a closed ticket, which nothing can be added to', async () => {
    getGuestTicket.mockResolvedValue(ticket('Closed'))
    getSiteConfig.mockResolvedValue({ name: 'x', logo_url: '', version: '1', guest_submission_enabled: true, guest_attachments_enabled: true, guest_attachment_max_bytes: 5 * 1024 * 1024 })
    renderWithQuery(<GuestTicketViewPage />)
    await screen.findByRole('note')
    expect(addFiles()).toBeNull()
  })

  it('uploads a chosen file with the link token and reports it attached', async () => {
    getSiteConfig.mockResolvedValue({ name: 'x', logo_url: '', version: '1', guest_submission_enabled: true, guest_attachments_enabled: true, guest_attachment_max_bytes: 5 * 1024 * 1024 })
    uploadGuestAttachment.mockResolvedValue(undefined)
    const { container } = renderWithQuery(<GuestTicketViewPage />)
    await screen.findByRole('button', { name: 'Add files' })
    const input = container.querySelector('input[type=file]') as HTMLInputElement
    const file = new File(['log'], 'printer.txt', { type: 'text/plain' })
    await userEvent.upload(input, file)
    await waitFor(() => expect(uploadGuestAttachment).toHaveBeenCalledWith('some-token', file))
    expect(await screen.findByRole('status')).toBeTruthy()
    expect(document.body.textContent).toContain('printer.txt')
  })

  it('does not send a file over the limit, and says which limit', async () => {
    getSiteConfig.mockResolvedValue({ name: 'x', logo_url: '', version: '1', guest_submission_enabled: true, guest_attachments_enabled: true, guest_attachment_max_bytes: 1024 * 1024 })
    const { container } = renderWithQuery(<GuestTicketViewPage />)
    await screen.findByRole('button', { name: 'Add files' })
    const input = container.querySelector('input[type=file]') as HTMLInputElement
    const big = new File([new Uint8Array(1024 * 1024 + 1)], 'big.txt', { type: 'text/plain' })
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    await userEvent.upload(input, big)
    expect(uploadGuestAttachment).not.toHaveBeenCalled()
    expect(window.alert).toHaveBeenCalledWith(expect.stringMatching(/1 MB/))
  })

  it("shows the server's reason when a file is refused", async () => {
    getSiteConfig.mockResolvedValue({ name: 'x', logo_url: '', version: '1', guest_submission_enabled: true, guest_attachments_enabled: true, guest_attachment_max_bytes: 5 * 1024 * 1024 })
    uploadGuestAttachment.mockRejectedValue(new GuestUploadRefused('this instance does not accept .exe files'))
    const { container } = renderWithQuery(<GuestTicketViewPage />)
    await screen.findByRole('button', { name: 'Add files' })
    const input = container.querySelector('input[type=file]') as HTMLInputElement
    await userEvent.upload(input, new File(['x'], 'a.txt', { type: 'text/plain' }))
    expect((await screen.findByRole('alert')).textContent).toMatch(/does not accept \.exe files/)
  })

  it('tracks two files with the same name separately', async () => {
    getSiteConfig.mockResolvedValue({ name: 'x', logo_url: '', version: '1', guest_submission_enabled: true, guest_attachments_enabled: true, guest_attachment_max_bytes: 5 * 1024 * 1024 })
    let release!: () => void
    uploadGuestAttachment.mockReturnValueOnce(new Promise<void>((r) => { release = r })).mockResolvedValue(undefined)
    const { container } = renderWithQuery(<GuestTicketViewPage />)
    await screen.findByRole('button', { name: 'Add files' })
    const input = container.querySelector('input[type=file]') as HTMLInputElement
    await userEvent.upload(input, [new File(['1'], 'a.txt', { type: 'text/plain' }), new File(['2'], 'a.txt', { type: 'text/plain' })])
    // The first is in flight and the second waits: one "Uploading", one "Queued".
    await screen.findByText('Uploading…')
    expect(screen.getAllByText('Uploading…')).toHaveLength(1)
    expect(screen.getAllByText('Queued')).toHaveLength(1)
    release()
    await waitFor(() => expect(uploadGuestAttachment).toHaveBeenCalledTimes(2))
  })
})
