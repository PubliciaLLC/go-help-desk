import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { updateAppearance } from '@/api/preferences'
import type { User } from '@/api/types'
import { useAuthStore } from '@/store/auth'
import { useThemeStore } from '@/store/theme'
import { AppearanceSection } from './AppearanceSection'

// Your account -> Appearance (#709): the three choices as labelled options. It
// saves on change, with no Save button, and every role has it.

vi.mock('@/api/preferences', () => ({ updateAppearance: vi.fn() }))

function me(role: User['role'], theme: User['theme'] = 'device'): User {
  return {
    id: 'u-1',
    email: 'sam@example.com',
    display_name: 'Sam',
    role,
    mfa_enabled: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    theme,
  }
}

beforeEach(() => {
  window.localStorage.clear()
  document.documentElement.classList.remove('dark')
  useThemeStore.setState({ preference: 'device', prefersDark: false, saving: false, pending: 0, error: null, saveCount: 0 })
})

describe('the Appearance section', () => {
  it.each(['user', 'staff', 'admin'] as const)('is there for a %s', (role) => {
    useAuthStore.setState({ user: me(role) })
    render(<AppearanceSection />)
    expect(screen.getByRole('heading', { name: 'Appearance' })).toBeTruthy()
    expect(screen.getByText('Saved to your account, so it follows you to other devices.')).toBeTruthy()
    for (const name of ['Light', 'Dark', 'Match my device']) expect(screen.getByRole('radio', { name })).toBeTruthy()
  })

  it('has no Save button', () => {
    useAuthStore.setState({ user: me('staff') })
    render(<AppearanceSection />)
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('saves on change and says "Saved."', async () => {
    useAuthStore.setState({ user: me('staff') })
    vi.mocked(updateAppearance).mockResolvedValue(me('staff', 'dark'))
    render(<AppearanceSection />)

    await userEvent.click(screen.getByRole('radio', { name: 'Dark' }))

    expect(updateAppearance).toHaveBeenCalledWith('dark')
    expect(await screen.findByText('Saved.')).toBeTruthy()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it("puts the choice back and says why when it cannot be saved", async () => {
    useAuthStore.setState({ user: me('staff') })
    vi.mocked(updateAppearance).mockRejectedValue(new Error('network'))
    render(<AppearanceSection />)

    await userEvent.click(screen.getByRole('radio', { name: 'Dark' }))

    expect(await screen.findByText("Couldn't save your appearance. Try again.")).toBeTruthy()
    expect((screen.getByRole('radio', { name: 'Match my device' }) as HTMLInputElement).checked).toBe(true)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
