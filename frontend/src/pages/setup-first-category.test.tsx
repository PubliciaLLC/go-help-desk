import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// Setup asks for the first category.
//
// Before this, a fresh instance finished setup with an empty `categories`
// table, and the first ticket its new administrator tried to file answered
// `400 category_id is required` — naming a field rather than the action they
// needed to take. Setup is the only moment anybody is being asked to
// configure anything, so it is where the question belongs (#323).

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))
vi.mock('@/api/setup', () => ({ setupAdmin: vi.fn(), getSetupStatus: vi.fn() }))

import { SetupPage } from './SetupPage'
import { setupAdmin } from '@/api/setup'

async function fillTheForm(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText('Full name'), 'Sam Admin')
  await user.type(screen.getByLabelText('Email'), 'admin@example.com')
  await user.type(screen.getByLabelText('Password'), 'correct-horse-battery')
  await user.type(screen.getByLabelText('Confirm password'), 'correct-horse-battery')
}

beforeEach(() => vi.mocked(setupAdmin).mockResolvedValue({} as never))
afterEach(() => vi.clearAllMocks())

describe('the setup wizard', () => {
  it('asks for a first category, pre-filled so nobody has to think about it', () => {
    renderWithQuery(<SetupPage />)
    const field = screen.getByLabelText('First category') as HTMLInputElement
    expect(field).toBeTruthy()
    expect(field.value, 'a blank field makes the operator invent an answer').not.toBe('')
  })

  it('sends the category along with the account', async () => {
    const user = userEvent.setup()
    renderWithQuery(<SetupPage />)
    await fillTheForm(user)

    const field = screen.getByLabelText('First category')
    await user.clear(field)
    await user.type(field, 'Hardware')
    await user.click(screen.getByRole('button', { name: /create admin account/i }))

    await waitFor(() =>
      expect(setupAdmin).toHaveBeenCalledWith(
        'admin@example.com', 'Sam Admin', 'correct-horse-battery', 'Hardware',
      ),
    )
  })

  it('still refuses a mismatched password, and asks the server nothing', async () => {
    const user = userEvent.setup()
    renderWithQuery(<SetupPage />)
    await user.type(screen.getByLabelText('Full name'), 'Sam Admin')
    await user.type(screen.getByLabelText('Email'), 'admin@example.com')
    await user.type(screen.getByLabelText('Password'), 'one-password')
    await user.type(screen.getByLabelText('Confirm password'), 'another-password')
    await user.click(screen.getByRole('button', { name: /create admin account/i }))

    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(setupAdmin).not.toHaveBeenCalled()
  })
})
