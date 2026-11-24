import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { ReplyComposer } from './ReplyComposer'
import * as ticketsApi from '@/api/tickets'
import * as authApi from '@/api/auth'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/api/types'

// #673: the composer's per-file limit is the instance's attachment_max_bytes,
// which only /me carries. The sign-in response's user does not have it, so the
// composer asks /me, and falls back to the store's copy while /me is pending.

vi.mock('@/components/editor/MarkdownEditor', () => ({
  MarkdownEditor: (p: { value: string; onChange: (v: string) => void }) => (
    <textarea value={p.value} onChange={(e) => p.onChange(e.target.value)} />
  ),
}))

const MB = 1024 * 1024

// A File that claims a size without allocating it.
function fileOf(name: string, bytes: number): File {
  const f = new File(['x'], name, { type: 'application/pdf' })
  Object.defineProperty(f, 'size', { value: bytes })
  return f
}

// What a password sign-in leaves in the store: no attachment_max_bytes.
function signInFromLoginResponse() {
  const user: User = {
    id: 'u-1',
    email: 'sam@example.com',
    display_name: 'Sam',
    role: 'staff',
    mfa_enabled: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
  useAuthStore.setState({ user })
}

function chooseFile(container: HTMLElement, file: File) {
  const input = container.querySelector('input[type="file"]') as HTMLInputElement
  fireEvent.change(input, { target: { files: [file] } })
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(ticketsApi, 'listTicketCannedResponses').mockResolvedValue([])
  vi.spyOn(window, 'alert').mockImplementation(() => {})
  signInFromLoginResponse()
})

describe('the composer per-file limit comes from /me', () => {
  it('accepts a 26 MB file once /me says the limit is 30 MB', async () => {
    vi.spyOn(authApi, 'getMe').mockResolvedValue({ attachment_max_bytes: 31457280 } as User)
    const { container, queryClient } = renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    await waitFor(() => expect(queryClient.getQueryState(['me'])?.status).toBe('success'))

    chooseFile(container, fileOf('report.pdf', 26 * MB))

    expect(window.alert).not.toHaveBeenCalled()
    expect(await screen.findByText('report.pdf')).toBeTruthy()
  })

  it('falls back to the 25 MB default when /me fails and the store has no limit', async () => {
    vi.spyOn(authApi, 'getMe').mockRejectedValue(new Error('network down'))
    const { container, queryClient } = renderWithQuery(<ReplyComposer ticketId="t" isStaffOrAdmin />)
    await waitFor(() => expect(queryClient.getQueryState(['me'])?.status).toBe('error'))

    chooseFile(container, fileOf('report.pdf', 26 * MB))

    expect(window.alert).toHaveBeenCalledTimes(1)
    expect(window.alert).toHaveBeenCalledWith(expect.stringContaining('25 MB'))
  })
})
