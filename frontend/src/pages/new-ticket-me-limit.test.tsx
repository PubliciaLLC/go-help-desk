import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import * as ticketsApi from '@/api/tickets'
import * as authApi from '@/api/auth'
import type { User } from '@/api/types'

// #673: the new-ticket page's per-file limit is the instance's
// attachment_max_bytes from /me. A password sign-in leaves a user without it in
// the store, so the page asks /me, and falls back to the store while /me is
// pending or failed.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
}))
vi.mock('@/components/Layout', () => ({
  Layout: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { NewTicketPage } from './NewTicketPage'

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
    role: 'user',
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
  vi.spyOn(window, 'alert').mockImplementation(() => {})
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue([] as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicTypes').mockResolvedValue([] as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicItems').mockResolvedValue([] as any)
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([])
  signInFromLoginResponse()
})

describe('the new-ticket per-file limit comes from /me', () => {
  it('accepts a 26 MB file once /me says the limit is 30 MB', async () => {
    vi.spyOn(authApi, 'getMe').mockResolvedValue({ attachment_max_bytes: 31457280 } as User)
    const { container, queryClient } = renderWithQuery(<NewTicketPage />)
    await waitFor(() => expect(queryClient.getQueryState(['me'])?.status).toBe('success'))

    chooseFile(container, fileOf('report.pdf', 26 * MB))

    expect(window.alert).not.toHaveBeenCalled()
    expect(await screen.findByText('report.pdf')).toBeTruthy()
  })

  it('falls back to the 25 MB default when /me fails and the store has no limit', async () => {
    vi.spyOn(authApi, 'getMe').mockRejectedValue(new Error('network down'))
    const { container, queryClient } = renderWithQuery(<NewTicketPage />)
    await waitFor(() => expect(queryClient.getQueryState(['me'])?.status).toBe('error'))

    chooseFile(container, fileOf('report.pdf', 26 * MB))

    expect(window.alert).toHaveBeenCalledTimes(1)
    expect(window.alert).toHaveBeenCalledWith(expect.stringContaining('25 MB'))
  })
})
