import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// The compelled-enrolment form, shared by the login page and the signup
// verification page (#369). Whoever renders it gets one promise: onEnrolled
// runs once the code is confirmed, and not before.

vi.mock('@/api/auth', () => ({ enrollMFAStart: vi.fn(), enrollMFAConfirm: vi.fn() }))

import { MFAEnrollForm } from './MFAEnrollForm'
import { enrollMFAStart, enrollMFAConfirm } from '@/api/auth'

function apiError(message: string) {
  return { isAxiosError: true, message, response: { data: { error: { code: 'x', message } } } }
}

beforeEach(() => {
  vi.mocked(enrollMFAStart).mockResolvedValue({ secret: 'SECRET', qr_url: '', qr_data_url: 'data:,' })
})
afterEach(() => vi.clearAllMocks())

describe('the enrolment form', () => {
  it('starts enrolment and holds the button until there is a secret', async () => {
    let release: (v: { secret: string; qr_url: string; qr_data_url: string }) => void = () => {}
    vi.mocked(enrollMFAStart).mockReturnValue(new Promise((r) => (release = r)))
    renderWithQuery(<MFAEnrollForm onEnrolled={vi.fn()} />)

    const button = screen.getByRole('button', { name: /confirm/i }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    release({ secret: 'SECRET', qr_url: '', qr_data_url: 'data:,' })
    await waitFor(() => expect(button.disabled).toBe(false))
    expect(screen.getByText(/SECRET/)).toBeTruthy()
  })

  it('confirms the code, then calls onEnrolled', async () => {
    vi.mocked(enrollMFAConfirm).mockResolvedValue(undefined)
    const onEnrolled = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderWithQuery(<MFAEnrollForm onEnrolled={onEnrolled} />)

    await user.type(screen.getByLabelText('Verification code'), '123456')
    await waitFor(() => expect((screen.getByRole('button', { name: /confirm/i }) as HTMLButtonElement).disabled).toBe(false))
    await user.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => expect(onEnrolled).toHaveBeenCalledTimes(1))
    expect(enrollMFAConfirm).toHaveBeenCalledWith('123456')
  })

  it('shows a refused code and does not call onEnrolled', async () => {
    vi.mocked(enrollMFAConfirm).mockRejectedValue(apiError('invalid code'))
    const onEnrolled = vi.fn()
    const user = userEvent.setup()
    renderWithQuery(<MFAEnrollForm onEnrolled={onEnrolled} />)

    await user.type(screen.getByLabelText('Verification code'), '000000')
    await waitFor(() => expect((screen.getByRole('button', { name: /confirm/i }) as HTMLButtonElement).disabled).toBe(false))
    await user.click(screen.getByRole('button', { name: /confirm/i }))

    expect((await screen.findByRole('alert')).textContent).toMatch(/invalid code/)
    expect(onEnrolled).not.toHaveBeenCalled()
  })
})
