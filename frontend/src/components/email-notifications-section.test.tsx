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

// An administrator can force a switch on or off for everyone (#441). The
// person sees what applies to them, locked, and who decided.
describe('a switch the administrator has forced', () => {
  const state = (enabled: boolean, forced: boolean) => ({ enabled, forced })
  const forcedProfile = () =>
    staff({
      email_notify_assigned: false, // their own choice: off
      email_notify_group: true, // their own choice: on
      email_notifications: {
        assigned: state(true, true), // forced on over their off
        activity: state(true, false),
        group: state(false, true), // forced off over their on
      },
    })

  it('shows what applies, not what they chose, and cannot be changed', () => {
    useAuthStore.setState({ user: forcedProfile() })
    renderWithQuery(<EmailNotificationsSection />)

    const assigned = screen.getByRole('checkbox', ASSIGNED) as HTMLInputElement
    expect(assigned.checked).toBe(true)
    expect(assigned.disabled).toBe(true)
    const group = screen.getByRole('checkbox', GROUP) as HTMLInputElement
    expect(group.checked).toBe(false)
    expect(group.disabled).toBe(true)
  })

  it('says it was set by the administrator, on that switch only', () => {
    useAuthStore.setState({ user: forcedProfile() })
    renderWithQuery(<EmailNotificationsSection />)

    expect(screen.getAllByText('Set by your administrator')).toHaveLength(2)
    const activity = screen.getByRole('checkbox', ACTIVITY) as HTMLInputElement
    expect(activity.disabled).toBe(false)
    expect(activity.closest('li')?.textContent).not.toMatch(/Set by your administrator/)
  })

  it('does not send anything when a locked switch is clicked', async () => {
    useAuthStore.setState({ user: forcedProfile() })
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: forcedProfile() })
    renderWithQuery(<EmailNotificationsSection />)

    await userEvent.click(screen.getByRole('checkbox', ASSIGNED))

    expect(patch).not.toHaveBeenCalled()
  })

  it('still saves a switch that is not forced', async () => {
    useAuthStore.setState({ user: forcedProfile() })
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: forcedProfile() })
    renderWithQuery(<EmailNotificationsSection />)

    await userEvent.click(screen.getByRole('checkbox', ACTIVITY))

    await waitFor(() => expect(patch).toHaveBeenCalledWith('/me/email-preferences', { email_notify_activity: false }))
  })

  it('shows the state the server reports after a save', async () => {
    useAuthStore.setState({ user: staff() })
    vi.spyOn(api, 'patch').mockResolvedValue({
      data: staff({
        email_notify_group: false,
        email_notifications: { assigned: state(true, false), activity: state(true, false), group: state(true, true) },
      }),
    })
    renderWithQuery(<EmailNotificationsSection />)

    await userEvent.click(screen.getByRole('checkbox', GROUP))

    // They turned it off, and the administrator has it forced on.
    await waitFor(() => expect(screen.getByText('Set by your administrator')).toBeTruthy())
    expect((screen.getByRole('checkbox', GROUP) as HTMLInputElement).checked).toBe(true)
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
