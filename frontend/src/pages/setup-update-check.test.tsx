import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// #170: setup asks, unchecked, whether this instance may check weekly for new
// releases. The wording is fixed by Erik: it says who is contacted and what is
// sent.

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('@/api/setup', () => ({ setupAdmin: vi.fn(), getSetupStatus: vi.fn() }))

import { SetupPage } from './SetupPage'
import { setupAdmin } from '@/api/setup'

const LABEL = 'Check for new releases (contacts GitHub once a week; sends nothing about your server)'

async function fillTheForm(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText('Full name'), 'Sam Admin')
  await user.type(screen.getByLabelText('Email'), 'admin@example.com')
  await user.type(screen.getByLabelText('Password'), 'correct-horse-battery')
  await user.type(screen.getByLabelText('Confirm password'), 'correct-horse-battery')
}

beforeEach(() => vi.mocked(setupAdmin).mockResolvedValue({} as never))
afterEach(() => vi.clearAllMocks())

describe('the update check question in setup', () => {
  it('is an unchecked checkbox with the agreed wording', () => {
    renderWithQuery(<SetupPage />)
    const box = screen.getByLabelText(LABEL) as HTMLInputElement
    expect(box.type).toBe('checkbox')
    expect(box.checked, 'opt-in: off until the administrator chooses').toBe(false)
  })

  it('sends nothing about it when left unchecked', async () => {
    const user = userEvent.setup()
    renderWithQuery(<SetupPage />)
    await fillTheForm(user)
    await user.click(screen.getByRole('button', { name: /create admin account/i }))
    await waitFor(() =>
      expect(setupAdmin).toHaveBeenCalledWith('admin@example.com', 'Sam Admin', 'correct-horse-battery', 'General'),
    )
  })

  it('stores the choice when ticked', async () => {
    const user = userEvent.setup()
    renderWithQuery(<SetupPage />)
    await fillTheForm(user)
    await user.click(screen.getByLabelText(LABEL))
    await user.click(screen.getByRole('button', { name: /create admin account/i }))
    await waitFor(() =>
      expect(setupAdmin).toHaveBeenCalledWith('admin@example.com', 'Sam Admin', 'correct-horse-battery', 'General', true),
    )
  })
})
