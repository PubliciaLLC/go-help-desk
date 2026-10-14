import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/api/types'
import { EmailNotificationsSection } from './EmailNotificationsSection'

// The three staff-email switches on a person's own profile (#441). Each is on
// by default; a switch is saved the moment it is flipped, and only the one
// flipped is sent.

function staff(over: Partial<User> = {}): User {
  return {
    id: 'u-1',
    email: 'sam@example.com',
    display_name: 'Sam',
    role: 'staff',
    mfa_enabled: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    email_notify_assigned: true,
    email_notify_activity: true,
    email_notify_group: true,
    ...over,
  }
}

const ASSIGNED = { name: /tickets assigned to me/i }
const ACTIVITY = { name: /activity on my tickets/i }
const GROUP = { name: /my groups' tickets/i }

beforeEach(() => {
  useAuthStore.setState({ user: staff() })
})
afterEach(() => vi.restoreAllMocks())

describe('the email switches', () => {
  it('shows all three, on by default', () => {
    renderWithQuery(<EmailNotificationsSection />)
    for (const q of [ASSIGNED, ACTIVITY, GROUP]) {
      expect((screen.getByRole('checkbox', q) as HTMLInputElement).checked).toBe(true)
    }
  })

  it('shows what is saved', () => {
    useAuthStore.setState({ user: staff({ email_notify_activity: false }) })
    renderWithQuery(<EmailNotificationsSection />)
    expect((screen.getByRole('checkbox', ASSIGNED) as HTMLInputElement).checked).toBe(true)
    expect((screen.getByRole('checkbox', ACTIVITY) as HTMLInputElement).checked).toBe(false)
    expect((screen.getByRole('checkbox', GROUP) as HTMLInputElement).checked).toBe(true)
  })

  it('reads a profile from before the switches existed as on', () => {
    useAuthStore.setState({ user: staff({ email_notify_assigned: undefined, email_notify_activity: undefined, email_notify_group: undefined }) })
    renderWithQuery(<EmailNotificationsSection />)
    for (const q of [ASSIGNED, ACTIVITY, GROUP]) {
      expect((screen.getByRole('checkbox', q) as HTMLInputElement).checked).toBe(true)
    }
  })

  it('saves only the switch that was flipped, and shows the saved profile', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: staff({ email_notify_group: false }) })
    renderWithQuery(<EmailNotificationsSection />)

    await userEvent.click(screen.getByRole('checkbox', GROUP))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/me/email-preferences', { email_notify_group: false })
    await waitFor(() => expect(useAuthStore.getState().user?.email_notify_group).toBe(false))
    expect((screen.getByRole('checkbox', GROUP) as HTMLInputElement).checked).toBe(false)
    expect((screen.getByRole('checkbox', ASSIGNED) as HTMLInputElement).checked).toBe(true)
  })

  it('turns a switch back on', async () => {
    useAuthStore.setState({ user: staff({ email_notify_assigned: false }) })
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: staff() })
    renderWithQuery(<EmailNotificationsSection />)

    await userEvent.click(screen.getByRole('checkbox', ASSIGNED))

    await waitFor(() => expect(patch).toHaveBeenCalledWith('/me/email-preferences', { email_notify_assigned: true }))
  })

  it('leaves the switch where it was and says so when the save fails', async () => {
    vi.spyOn(api, 'patch').mockRejectedValue(new Error('network'))
    renderWithQuery(<EmailNotificationsSection />)

    await userEvent.click(screen.getByRole('checkbox', ACTIVITY))

    expect(await screen.findByRole('alert')).toBeTruthy()
    expect((screen.getByRole('checkbox', ACTIVITY) as HTMLInputElement).checked).toBe(true)
    expect(useAuthStore.getState().user?.email_notify_activity).toBe(true)
  })

  it('says an administrator can force a switch for everyone', () => {
    renderWithQuery(<EmailNotificationsSection />)
    expect(screen.getByText(/administrator can turn any of these on or off for everyone/i)).toBeTruthy()
  })
})

describe('who sees it', () => {
  it('is for staff and administrators', () => {
    for (const role of ['staff', 'admin'] as const) {
      useAuthStore.setState({ user: staff({ role }) })
      const { unmount } = renderWithQuery(<EmailNotificationsSection />)
      expect(screen.queryByRole('checkbox', ASSIGNED)).not.toBeNull()
      unmount()
    }
  })

  it('is not shown to a requester, who is never sent staff mail', () => {
    useAuthStore.setState({ user: staff({ role: 'user' }) })
    renderWithQuery(<EmailNotificationsSection />)
    expect(screen.queryByRole('checkbox')).toBeNull()
  })
})
