import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'

// #170 and the 1.3.0 post-upgrade wizard: one heading per step, each step shows
// today's setting, says what changed, and saves through the ordinary settings
// API. Skip leaves the setting alone. Finishing or skipping the last step
// records the wizard as seen.

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => navigate }))

import { AfterUpgradePage } from './AfterUpgradePage'

const UPDATE_LABEL = 'Check for new releases (contacts GitHub once a week; sends nothing about your server)'

function serve(settings: Record<string, unknown> = {}, opts: { email?: boolean; old?: number } = {}) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/security-warnings') {
      return Promise.resolve({ data: { insecure_secrets: [], email: { enabled: opts.email ?? true } } })
    }
    if (url === '/admin/post-upgrade') return Promise.resolve({ data: { old_password_accounts: opts.old ?? 0 } })
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
}

// What an upgrade from 1.2 leaves behind: migration 000052 turned the three
// staff-email overrides off.
const UPGRADED = {
  staff_email_assigned_override: 'off',
  staff_email_activity_override: 'off',
  staff_email_group_override: 'off',
}

const heading = () => screen.getByRole('heading', { level: 2 }).textContent
const button = (name: string) => screen.getByRole('button', { name })
const patches = (patch: ReturnType<typeof serve>) => patch.mock.calls.map((c) => c[1])

async function opened() {
  renderWithQuery(<AfterUpgradePage />)
  await screen.findByRole('heading', { level: 2 })
}

beforeEach(() => navigate.mockClear())
afterEach(() => vi.restoreAllMocks())

describe('the post-upgrade wizard', () => {
  it('has one h1, then one h2 per step, in the agreed order', async () => {
    serve(UPGRADED)
    const user = userEvent.setup()
    await opened()
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1)

    const seen: (string | null)[] = []
    for (let i = 0; i < 4; i++) {
      seen.push(heading())
      await user.click(button(i === 3 ? 'Finish' : 'Next'))
      if (i < 3) await waitFor(() => expect(heading()).not.toBe(seen[i]))
    }
    expect(seen).toEqual([
      'Weekly check for new releases',
      'Staff email notifications',
      'Staff API keys',
      'Reset window for old passwords',
    ])
  })

  it('step 1 shows today’s setting, unchecked, and saves a ticked box', async () => {
    const patch = serve(UPGRADED)
    const user = userEvent.setup()
    await opened()
    const box = screen.getByLabelText(UPDATE_LABEL) as HTMLInputElement
    expect(box.type).toBe('checkbox')
    expect(box.checked).toBe(false)
    expect(screen.getByText(/currently: off/i)).toBeTruthy()

    await user.click(box)
    await user.click(button('Next'))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings', { update_check_enabled: true })
    expect(await screen.findByRole('heading', { level: 2, name: 'Staff email notifications' })).toBeTruthy()
  })

  it('Skip leaves a step alone even after it was edited', async () => {
    const patch = serve(UPGRADED)
    const user = userEvent.setup()
    await opened()
    await user.click(screen.getByLabelText(UPDATE_LABEL))
    await user.click(button('Skip'))
    await screen.findByRole('heading', { level: 2, name: 'Staff email notifications' })
    expect(patch).not.toHaveBeenCalled()

    // And a later save does not carry the skipped step's edit along.
    await user.selectOptions(screen.getByLabelText(/staff email/i), 'on')
    await user.click(button('Next'))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patches(patch)[0]).not.toHaveProperty('update_check_enabled')
  })

  it('step 2 says what 1.3.0 did, shows today’s value and saves all three overrides', async () => {
    const patch = serve(UPGRADED)
    const user = userEvent.setup()
    await opened()
    await user.click(button('Skip'))
    expect(screen.getByText(/turned them off/i)).toBeTruthy()
    expect(screen.getByText(/currently: off for everyone/i)).toBeTruthy()

    await user.selectOptions(screen.getByLabelText(/staff email/i), 'user')
    await user.click(button('Next'))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings', {
      staff_email_assigned_override: 'user',
      staff_email_activity_override: 'user',
      staff_email_group_override: 'user',
    })
  })

  it('step 3 saves the staff-keys switch and the lifetime', async () => {
    const patch = serve({ ...UPGRADED, staff_can_create_api_keys: false })
    const user = userEvent.setup()
    await opened()
    await user.click(button('Skip'))
    await user.click(button('Skip'))
    expect(heading()).toBe('Staff API keys')

    await user.click(screen.getByLabelText(/staff may create their own api keys/i))
    // The slider has six stops: 1..5 years and Never. Move it to 3 years.
    fireEvent.change(screen.getByRole('slider'), { target: { value: '3' } })
    await user.click(button('Next'))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings', {
      staff_can_create_api_keys: true,
      api_key_max_lifetime_years: 3,
    })
  })

  it('step 4 shows the count and email status, and saves the window', async () => {
    const patch = serve({ ...UPGRADED, password_rehash_grace_days: 30 }, { old: 7, email: false })
    const user = userEvent.setup()
    await opened()
    for (let i = 0; i < 3; i++) await user.click(button('Skip'))
    expect(heading()).toBe('Reset window for old passwords')
    await waitFor(() => expect(screen.getByText(/7 accounts/i)).toBeTruthy())
    expect(screen.getByText(/email is not set up/i)).toBeTruthy()

    const days = screen.getByLabelText(/days before an old password must be reset/i)
    await user.clear(days)
    await user.type(days, '60')
    await user.click(button('Finish'))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    // The last step's change and the seen flag go in one write.
    expect(patch).toHaveBeenCalledWith('/admin/settings', {
      password_rehash_grace_days: 60,
      post_upgrade_130_seen: true,
    })
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('step 4 says reset links can be mailed when email is set up', async () => {
    serve(UPGRADED, { old: 1, email: true })
    const user = userEvent.setup()
    await opened()
    for (let i = 0; i < 3; i++) await user.click(button('Skip'))
    await waitFor(() => expect(screen.getByText(/1 account\b/i)).toBeTruthy())
    expect(screen.getByText(/reset links can be emailed/i)).toBeTruthy()
  })

  it('skipping every step changes nothing but records the wizard as seen', async () => {
    const patch = serve(UPGRADED)
    const user = userEvent.setup()
    await opened()
    for (let i = 0; i < 4; i++) await user.click(button('Skip'))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings', { post_upgrade_130_seen: true })
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('Back returns to the previous step', async () => {
    serve(UPGRADED)
    const user = userEvent.setup()
    await opened()
    expect((button('Back') as HTMLButtonElement).disabled).toBe(true)
    await user.click(button('Skip'))
    await user.click(button('Back'))
    expect(heading()).toBe('Weekly check for new releases')
  })

  it('moves focus to the new step’s heading so a screen reader announces it', async () => {
    serve(UPGRADED)
    const user = userEvent.setup()
    await opened()
    await user.click(button('Skip'))
    expect(document.activeElement).toBe(screen.getByRole('heading', { level: 2 }))
    expect(screen.getByText(/step 2 of 4/i)).toBeTruthy()
  })

  it('keeps the administrator on the step and says why when the server refuses', async () => {
    const patch = serve(UPGRADED)
    patch.mockRejectedValueOnce({ isAxiosError: true, response: { data: { error: { message: 'nope' } } } })
    const user = userEvent.setup()
    await opened()
    await user.click(screen.getByLabelText(UPDATE_LABEL))
    await user.click(button('Next'))
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(heading()).toBe('Weekly check for new releases')
  })
})

// Step 5 (#536): guest file uploads. It only makes sense while guest submission
// is on, so it is part of the wizard only then.
describe('the guest file uploads step', () => {
  const GUESTS_ON = { ...UPGRADED, guest_submission_enabled: true }

  async function toStep5(user: ReturnType<typeof userEvent.setup>) {
    for (let i = 0; i < 4; i++) await user.click(button('Skip'))
    expect(heading()).toBe('Guest file uploads')
  }

  it('comes fifth, says what changed, and shows today\u2019s setting (off)', async () => {
    serve(GUESTS_ON)
    const user = userEvent.setup()
    await opened()
    expect(screen.getByText(/step 1 of 5/i)).toBeTruthy()
    await toStep5(user)
    expect(screen.getByText(/in 1\.2, a guest could upload files through the api/i)).toBeTruthy()
    expect(screen.getByText(/1\.3\.0 switches that off/i)).toBeTruthy()
    expect(screen.getByText(/currently: guest uploads are off/i)).toBeTruthy()
    expect(button('Finish')).toBeTruthy()
  })

  it('saves the switch, size, per-ticket and per-hour limits through the settings API', async () => {
    const patch = serve(GUESTS_ON)
    const user = userEvent.setup()
    await opened()
    await toStep5(user)

    // The limits are unavailable until uploads are switched on.
    expect((screen.getByRole('slider') as HTMLInputElement).disabled).toBe(true)
    await user.click(screen.getByLabelText('Guests may attach files'))
    fireEvent.change(screen.getByRole('slider'), { target: { value: '10' } })
    const perTicket = screen.getByLabelText('Most files a guest can attach to one ticket')
    await user.clear(perTicket)
    await user.type(perTicket, '8')
    const perHour = screen.getByLabelText('Uploads per guest link per hour')
    await user.clear(perHour)
    await user.type(perHour, '6')
    await user.click(button('Finish'))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings', {
      guest_attachments_enabled: true,
      guest_attachment_max_mb: 10,
      guest_attachment_max_per_ticket: 8,
      guest_attachment_uploads_per_hour: 6,
      post_upgrade_130_seen: true,
    })
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('Skip leaves uploads off: nothing but the seen flag is sent', async () => {
    const patch = serve(GUESTS_ON)
    const user = userEvent.setup()
    await opened()
    await toStep5(user)
    await user.click(screen.getByLabelText('Guests may attach files'))
    await user.click(button('Skip'))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings', { post_upgrade_130_seen: true })
  })

  it.each([
    ['off', { ...UPGRADED, guest_submission_enabled: false }],
    ['never set', UPGRADED],
  ])('is not a step while guest submission is %s, and the wizard says why', async (_n, settings) => {
    serve(settings)
    const user = userEvent.setup()
    await opened()
    expect(screen.getByText(/step 1 of 4/i)).toBeTruthy()
    for (let i = 0; i < 3; i++) await user.click(button('Skip'))
    expect(heading()).toBe('Reset window for old passwords')
    expect(screen.getByText(/guest file uploads stay off/i)).toBeTruthy()
    expect(screen.getByText(/need guest submission/i)).toBeTruthy()
    expect(button('Finish')).toBeTruthy()
  })

  it('does not mention it when guest submission is on', async () => {
    serve(GUESTS_ON)
    const user = userEvent.setup()
    await opened()
    for (let i = 0; i < 3; i++) await user.click(button('Skip'))
    expect(screen.queryByText(/guest file uploads stay off/i)).toBeNull()
  })
})
