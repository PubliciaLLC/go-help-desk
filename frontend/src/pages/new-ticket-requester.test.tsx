import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import * as ticketsApi from '@/api/tickets'

// #444: staff and administrators choose who a ticket is for. The default is
// themselves; they can pick an existing user (search as you type) or type an
// address for someone with no account. A requester is offered none of this.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
}))
vi.mock('@/components/Layout', () => ({
  Layout: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { NewTicketPage } from './NewTicketPage'

const CATEGORIES = [{ id: 'cat-1', name: 'Network', active: true, sort_order: 1 }]
const ADA = { id: 'u-ada', display_name: 'Ada Lovelace', email: 'ada@example.com' }
const GRACE = { id: 'u-grace', display_name: 'Grace Hopper', email: 'grace@example.com' }

function signInAs(role: 'user' | 'staff' | 'admin') {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: `${role}@example.com`,
      display_name: 'Sam',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

let createTicket: ReturnType<typeof vi.spyOn>
let lookup: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  vi.restoreAllMocks()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue(CATEGORIES as any)
  vi.spyOn(ticketsApi, 'listPublicTypes').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'listPublicItems').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([])
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  createTicket = vi.spyOn(ticketsApi, 'createTicket').mockResolvedValue({ id: 't-1' } as any)
  lookup = vi.spyOn(ticketsApi, 'lookupRequesters').mockImplementation(async (q: string) =>
    [ADA, GRACE].filter((u) => u.display_name.toLowerCase().includes(q.toLowerCase())),
  )
})

async function fillTicket(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/subject/i), 'Printer on 3')
  await screen.findByRole('option', { name: 'Network' })
  await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
}

describe('a requester', () => {
  it('is offered no requester control and files for themselves', async () => {
    signInAs('user')
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)

    expect(screen.queryByText(/^requester$/i)).toBeNull()
    expect(screen.queryByLabelText(/another user/i)).toBeNull()
    expect(screen.queryByLabelText(/email address/i)).toBeNull()

    await user.click(screen.getByRole('button', { name: /submit ticket/i }))
    await waitFor(() => expect(createTicket).toHaveBeenCalledTimes(1))
    const sent = createTicket.mock.calls[0][0]
    expect(sent).not.toHaveProperty('reporter_user_id')
    expect(sent).not.toHaveProperty('reporter_email')
    expect(lookup).not.toHaveBeenCalled()
  })
})

describe.each(['staff', 'admin'] as const)('the requester control for %s', (role) => {
  it('defaults to me and sends no reporter field', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)

    expect((screen.getByRole('radio', { name: /^me$/i }) as HTMLInputElement).checked).toBe(true)
    await user.click(screen.getByRole('button', { name: /submit ticket/i }))
    await waitFor(() => expect(createTicket).toHaveBeenCalledTimes(1))
    const sent = createTicket.mock.calls[0][0]
    expect(sent).not.toHaveProperty('reporter_user_id')
    expect(sent).not.toHaveProperty('reporter_email')
  })

  it('searches existing users as you type and files for the one picked', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)

    await user.click(screen.getByRole('radio', { name: /another user/i }))
    const search = screen.getByLabelText(/search users/i)

    // One character is not a search: nothing is asked of the server.
    await user.type(search, 'a')
    await new Promise((r) => setTimeout(r, 450))
    expect(lookup).not.toHaveBeenCalled()

    await user.type(search, 'da')
    await user.click(await screen.findByRole('button', { name: /ada lovelace/i }))
    expect(lookup).toHaveBeenLastCalledWith('ada')
    expect(screen.getByText(/filing for/i).textContent).toMatch(/Ada Lovelace/)

    await user.click(screen.getByRole('button', { name: /submit ticket/i }))
    await waitFor(() => expect(createTicket).toHaveBeenCalledTimes(1))
    const sent = createTicket.mock.calls[0][0]
    expect(sent.reporter_user_id).toBe('u-ada')
    expect(sent).not.toHaveProperty('reporter_email')
  })

  it('will not submit "another user" until someone is picked', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)

    await user.click(screen.getByRole('radio', { name: /another user/i }))
    await user.click(screen.getByRole('button', { name: /submit ticket/i }))

    expect((await screen.findByRole('alert')).textContent).toMatch(/pick a requester/i)
    expect(createTicket).not.toHaveBeenCalled()
  })

  it('files for a typed address', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)

    await user.click(screen.getByRole('radio', { name: /email address/i }))
    await user.type(screen.getByLabelText(/requester email/i), '  new.person@example.com ')
    await user.type(screen.getByLabelText(/requester name/i), ' Ada Lovelace ')
    await user.type(screen.getByLabelText(/requester phone/i), '555-0100')
    expect(screen.getByText(/sent a link/i)).toBeTruthy()
    await user.click(screen.getByRole('button', { name: /submit ticket/i }))

    await waitFor(() => expect(createTicket).toHaveBeenCalledTimes(1))
    const sent = createTicket.mock.calls[0][0]
    expect(sent.reporter_email).toBe('new.person@example.com')
    expect(sent.guest_name).toBe('Ada Lovelace')
    expect(sent.guest_phone).toBe('555-0100')
    expect(sent).not.toHaveProperty('reporter_user_id')
  })

  it('files for a typed address without a phone number', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)

    await user.click(screen.getByRole('radio', { name: /email address/i }))
    await user.type(screen.getByLabelText(/requester email/i), 'new.person@example.com')
    await user.type(screen.getByLabelText(/requester name/i), 'Ada')
    await user.click(screen.getByRole('button', { name: /submit ticket/i }))

    await waitFor(() => expect(createTicket).toHaveBeenCalledTimes(1))
    expect(createTicket.mock.calls[0][0]).not.toHaveProperty('guest_phone')
  })

  it('will not submit a typed address without a name', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)

    await user.click(screen.getByRole('radio', { name: /email address/i }))
    await user.type(screen.getByLabelText(/requester email/i), 'new.person@example.com')
    await user.click(screen.getByRole('button', { name: /submit ticket/i }))

    expect((await screen.findByRole('alert')).textContent).toMatch(/name/i)
    expect(createTicket).not.toHaveBeenCalled()
  })

  it('will not submit an empty address', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)

    await user.click(screen.getByRole('radio', { name: /email address/i }))
    await user.click(screen.getByRole('button', { name: /submit ticket/i }))

    expect((await screen.findByRole('alert')).textContent).toMatch(/email address/i)
    expect(createTicket).not.toHaveBeenCalled()
  })

  it('goes back to me and forgets what was chosen', async () => {
    signInAs(role)
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)

    await user.click(screen.getByRole('radio', { name: /email address/i }))
    await user.type(screen.getByLabelText(/requester email/i), 'new.person@example.com')
    await user.type(screen.getByLabelText(/requester name/i), 'Ada')
    await user.click(screen.getByRole('radio', { name: /^me$/i }))
    await user.click(screen.getByRole('button', { name: /submit ticket/i }))

    await waitFor(() => expect(createTicket).toHaveBeenCalledTimes(1))
    expect(createTicket.mock.calls[0][0]).not.toHaveProperty('reporter_email')
    expect(createTicket.mock.calls[0][0]).not.toHaveProperty('guest_name')
  })

  it('shows the server\'s reason when the ticket is refused', async () => {
    signInAs(role)
    createTicket.mockRejectedValueOnce({
      isAxiosError: true,
      response: { data: { error: { code: 'bad_request', message: 'reporter_email has no account, and guest submission is off' } } },
    })
    const user = userEvent.setup()
    renderWithQuery(<NewTicketPage />)
    await fillTicket(user)
    await user.click(screen.getByRole('radio', { name: /email address/i }))
    await user.type(screen.getByLabelText(/requester email/i), 'new.person@example.com')
    await user.type(screen.getByLabelText(/requester name/i), 'Ada')
    await user.click(screen.getByRole('button', { name: /submit ticket/i }))

    expect((await screen.findByRole('alert')).textContent).toMatch(/guest submission is off/)
  })
})
