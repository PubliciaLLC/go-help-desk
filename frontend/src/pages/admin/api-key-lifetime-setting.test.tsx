import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #450: the longest an API key may live. A slider with six stops, 1 to 5 years
// and Never. Stored as the number of years, or -1 for Never. Default 2. A red
// warning shows whenever it is above 2, Never included.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

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

async function renderSettings(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await userEvent.click(screen.getByRole('button', { name: 'Authentication' }))
  return patch
}

const slider = () => screen.getByRole('slider', { name: /longest an api key may live/i }) as HTMLInputElement
const warning = () => screen.queryByRole('alert', { name: /longer than 2 years/i })

describe('the API key lifetime slider', () => {
  it('has six stops: 1 to 5 years, then Never, all labelled', async () => {
    await renderSettings({})
    expect(slider().min).toBe('1')
    expect(slider().max).toBe('6')
    expect(slider().step).toBe('1')
    const ticks = screen.getByTestId('lifetime-ticks')
    expect(Array.from(ticks.children).map((c) => c.textContent)).toEqual(
      ['1 year', '2 years', '3 years', '4 years', '5 years', 'Never'])
  })

  it('is at 2 years by default, which is not warned about', async () => {
    await renderSettings({})
    expect(slider().value).toBe('2')
    expect(slider().getAttribute('aria-valuetext')).toBe('2 years')
    expect(warning()).toBeNull()
  })

  it.each([
    [1, '1', '1 year', false],
    [2, '2', '2 years', false],
    [3, '3', '3 years', true],
    [5, '5', '5 years', true],
    [-1, '6', 'No limit', true],
  ])('shows a stored %s as stop %s, reads "%s", warns: %s', async (stored, stop, text, warns) => {
    await renderSettings({ api_key_max_lifetime_years: stored })
    expect(slider().value).toBe(stop)
    expect(slider().getAttribute('aria-valuetext')).toBe(text)
    expect(!!warning()).toBe(warns)
  })

  it('moves one stop at a time with the arrow keys, and the warning follows', async () => {
    await renderSettings({})
    slider().focus()
    await userEvent.keyboard('{ArrowRight}')
    expect(slider().value).toBe('3')
    expect(slider().getAttribute('aria-valuetext')).toBe('3 years')
    expect(warning()).toBeTruthy()
    await userEvent.keyboard('{ArrowLeft}')
    expect(slider().value).toBe('2')
    expect(warning()).toBeNull()
    await userEvent.keyboard('{ArrowUp}{ArrowUp}{ArrowUp}{ArrowUp}{ArrowUp}')
    expect(slider().value).toBe('6')
    expect(slider().getAttribute('aria-valuetext')).toBe('No limit')
    await userEvent.keyboard('{ArrowRight}')
    expect(slider().value).toBe('6')
    await userEvent.keyboard('{ArrowDown}')
    expect(slider().value).toBe('5')
  })

  it('saves the number of years, and -1 for Never', async () => {
    const patch = await renderSettings({})
    slider().focus()
    await userEvent.keyboard('{ArrowRight}')
    await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls.at(-1)![1]).toMatchObject({ api_key_max_lifetime_years: 3 })

    slider().focus()
    await userEvent.keyboard('{End}')
    await userEvent.keyboard('{ArrowUp}{ArrowUp}{ArrowUp}{ArrowUp}{ArrowUp}')
    await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)
    await waitFor(() => expect(patch.mock.calls.at(-1)![1]).toMatchObject({ api_key_max_lifetime_years: -1 }))
  })
})
