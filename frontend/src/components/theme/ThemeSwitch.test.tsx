import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { updateAppearance } from '@/api/preferences'
import { useAuthStore } from '@/store/auth'
import { SAVE_ERROR, useThemeStore } from '@/store/theme'
import { ThemeSwitch } from './ThemeSwitch'

// The switch (#709): a native radio group, so Tab reaches it once and the arrow
// keys move between and select the options. It is the same control in the
// sidebar and on the sign-in page (icons) and in Your account (labelled).

vi.mock('@/api/preferences', () => ({ updateAppearance: vi.fn() }))

beforeEach(() => {
  window.localStorage.clear()
  document.documentElement.classList.remove('dark')
  useAuthStore.setState({ user: null })
  useThemeStore.setState({ preference: 'device', prefersDark: false, saving: false, pending: 0, error: null, saveCount: 0 })
})

const radio = (name: string) => screen.getByRole('radio', { name }) as HTMLInputElement
const checked = (name: string) => radio(name).checked
const group = () => screen.getByRole('group', { name: 'Appearance' })

describe.each(['icons', 'labelled'] as const)('the %s switch', (variant) => {
  it('is a group named Appearance with three radios named Light, Dark and Match my device', () => {
    render(<ThemeSwitch variant={variant} />)
    expect(within(group()).getAllByRole('radio')).toHaveLength(3)
    for (const name of ['Light', 'Dark', 'Match my device']) expect(radio(name)).toBeTruthy()
  })

  it('shows the stored choice as checked', () => {
    useThemeStore.setState({ preference: 'dark' })
    render(<ThemeSwitch variant={variant} />)
    expect(checked('Dark')).toBe(true)
    expect(checked('Light')).toBe(false)
  })

  it('selects on a click', async () => {
    render(<ThemeSwitch variant={variant} />)
    await userEvent.click(screen.getByRole('radio', { name: 'Dark' }))
    expect(useThemeStore.getState().preference).toBe('dark')
    expect(checked('Dark')).toBe(true)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('is one tab stop, and the arrow keys move and select', async () => {
    useThemeStore.setState({ preference: 'light' })
    render(
      <>
        <button>before</button>
        <ThemeSwitch variant={variant} />
        <button>after</button>
      </>,
    )
    const user = userEvent.setup()
    await user.tab() // before
    await user.tab() // into the group, at the checked option
    expect(document.activeElement).toBe(radio('Light'))

    await user.keyboard('{ArrowRight}')
    expect(document.activeElement).toBe(radio('Dark'))
    expect(checked('Dark')).toBe(true)
    expect(useThemeStore.getState().preference).toBe('dark')

    await user.keyboard('{ArrowRight}')
    expect(useThemeStore.getState().preference).toBe('device')

    await user.tab() // out of the group, not to the next radio
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'after' }))
  })

  it('has the tooltip words on each option', () => {
    render(<ThemeSwitch variant={variant} />)
    for (const name of ['Light', 'Dark', 'Match my device']) {
      expect(radio(name).closest('label')?.getAttribute('title')).toBe(name)
    }
  })

  it('says the error in a polite live region when a save fails, and is quiet before that', async () => {
    useAuthStore.setState({ user: { id: 'u', email: 'a@b.c', display_name: 'A', role: 'user', mfa_enabled: false, created_at: '', updated_at: '' } })
    vi.mocked(updateAppearance).mockRejectedValue(new Error('x'))
    render(<ThemeSwitch variant={variant} />)
    const live = group().parentElement!.querySelector('[aria-live="polite"]')!
    expect(live.textContent).toBe('')

    await userEvent.click(screen.getByRole('radio', { name: 'Dark' }))
    expect(live.textContent).toBe(SAVE_ERROR)
    expect(checked('Light')).toBe(false) // put back to the previous (device)
    expect(checked('Match my device')).toBe(true)
  })
})

describe('the icons switch', () => {
  it('marks the checked option by more than a fill: the selected colours and a ring-coloured border', () => {
    useThemeStore.setState({ preference: 'dark' })
    render(<ThemeSwitch variant="icons" />)
    const cls = radio('Dark').closest('label')!.className
    expect(cls).toContain('has-[:checked]:bg-selected')
    expect(cls).toContain('has-[:checked]:text-selected-fg')
    expect(cls).toContain('has-[:checked]:border-ring')
  })

  it('keeps its segments narrow enough on a coarse pointer for Sign out to share the row', () => {
    render(<ThemeSwitch variant="icons" />)
    const cls = radio('Dark').closest('label')!.className
    expect(cls).toContain('pointer-coarse:h-11')
    expect(cls).toContain('pointer-coarse:w-8')
  })

  it('leaves the announcing to the labelled switch when both are on the page', async () => {
    useAuthStore.setState({ user: { id: 'u', email: 'a@b.c', display_name: 'A', role: 'user', mfa_enabled: false, created_at: '', updated_at: '' } })
    vi.mocked(updateAppearance).mockRejectedValue(new Error('x'))
    const { container } = render(
      <>
        <div data-testid="side"><ThemeSwitch variant="icons" /></div>
        <div data-testid="main"><ThemeSwitch variant="labelled" /></div>
      </>,
    )
    await userEvent.click(within(screen.getByTestId('main')).getByRole('radio', { name: 'Dark' }))
    const live = container.querySelectorAll('[aria-live="polite"]')
    expect(live).toHaveLength(1)
    expect(live[0].textContent).toBe(SAVE_ERROR)
    expect(screen.getByTestId('side').textContent).toContain(SAVE_ERROR) // still in the page for a screen reader to read, just not announced twice
  })

  it('shows a failed save as a tooltip and a red frame, without growing the row', async () => {
    useAuthStore.setState({ user: { id: 'u', email: 'a@b.c', display_name: 'A', role: 'user', mfa_enabled: false, created_at: '', updated_at: '' } })
    vi.mocked(updateAppearance).mockRejectedValue(new Error('x'))
    render(<ThemeSwitch variant="icons" />)
    await userEvent.click(screen.getByRole('radio', { name: 'Dark' }))
    expect(group().getAttribute('title')).toBe(SAVE_ERROR)
    expect(group().className).toContain('border-danger-border')
  })
})

describe('two switches on one page', () => {
  it('do not share a radio group', async () => {
    render(
      <>
        <div data-testid="a"><ThemeSwitch variant="icons" /></div>
        <div data-testid="b"><ThemeSwitch variant="icons" /></div>
      </>,
    )
    const a = within(screen.getByTestId('a')).getAllByRole('radio') as HTMLInputElement[]
    const b = within(screen.getByTestId('b')).getAllByRole('radio') as HTMLInputElement[]
    expect(new Set(a.map((r) => r.name)).size).toBe(1)
    expect(a[0].name).not.toBe(b[0].name)

    // Both are driven by the store, so they agree, and neither un-checks the other's group.
    await userEvent.click(a[1])
    expect(a[1].checked).toBe(true)
    expect(b[1].checked).toBe(true)
  })
})

describe('the labelled switch', () => {
  it('shows the words, and says "Saved." for a moment after a save', async () => {
    useAuthStore.setState({ user: { id: 'u', email: 'a@b.c', display_name: 'A', role: 'user', mfa_enabled: false, created_at: '', updated_at: '' } })
    vi.mocked(updateAppearance).mockResolvedValue({ id: 'u', email: 'a@b.c', display_name: 'A', role: 'user', mfa_enabled: false, created_at: '', updated_at: '', theme: 'dark' })
    render(<ThemeSwitch variant="labelled" />)
    expect(screen.getByText('Light')).toBeTruthy()
    expect(screen.getByText('Match my device')).toBeTruthy()
    expect(screen.queryByText('Saved.')).toBeNull()

    await userEvent.click(screen.getByRole('radio', { name: 'Dark' }))
    expect(await screen.findByText('Saved.')).toBeTruthy()
  })

  it('stops saying "Saved." after three seconds', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      useAuthStore.setState({ user: { id: 'u', email: 'a@b.c', display_name: 'A', role: 'user', mfa_enabled: false, created_at: '', updated_at: '' } })
      vi.mocked(updateAppearance).mockResolvedValue({ id: 'u', email: 'a@b.c', display_name: 'A', role: 'user', mfa_enabled: false, created_at: '', updated_at: '', theme: 'dark' })
      render(<ThemeSwitch variant="labelled" />)
      await userEvent.click(screen.getByRole('radio', { name: 'Dark' }))
      expect(await screen.findByText('Saved.')).toBeTruthy()
      await act(() => vi.advanceTimersByTimeAsync(3100))
      expect(screen.queryByText('Saved.')).toBeNull()
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not say "Saved." for a choice that was never sent (not signed in)', async () => {
    render(<ThemeSwitch variant="labelled" />)
    await userEvent.click(screen.getByRole('radio', { name: 'Dark' }))
    expect(screen.queryByText('Saved.')).toBeNull()
  })
})
