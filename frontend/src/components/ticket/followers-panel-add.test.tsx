import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { FollowersPanel } from './FollowersPanel'

// #695: staff and administrators add other staff as followers and remove any
// follower. The panel does not guess for other people (the server may refuse
// on scope), so the list changes only after the server agrees.

const TICKET = 'tkt-1'
const ME = { user_id: 'u-me', display_name: 'Sam Staff', created_at: '2026-10-01T10:00:00Z' }
const ALEX = { user_id: 'u-alex', display_name: 'Alex Admin', created_at: '2026-10-01T09:00:00Z' }

const STAFF = [
  { id: 'u-me', display_name: 'Sam Staff', assignable: true },
  { id: 'u-alex', display_name: 'Alex Admin', assignable: true },
  { id: 'u-bea', display_name: 'Bea Staff', assignable: true },
  { id: 'u-cal', display_name: 'Cal Colleague', assignable: true },
  { id: 'u-gone', display_name: 'Gone Suspended', assignable: false },
]

function signIn() {
  useAuthStore.setState({
    user: {
      id: 'u-me', email: 'sam@example.com', display_name: 'Sam Staff', role: 'staff',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function serve(followers: unknown[]) {
  let current = followers
  const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET}/followers`) return Promise.resolve({ data: current })
    if (url === '/staff') return Promise.resolve({ data: STAFF })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  return { get, set: (next: unknown[]) => { current = next } }
}

function refusal(status: number, code: string, message: string) {
  return { isAxiosError: true, response: { status, data: { error: { code, message } } } }
}

beforeEach(signIn)
afterEach(() => vi.restoreAllMocks())

async function picker() {
  return (await screen.findByRole('combobox', { name: 'Add follower' })) as HTMLSelectElement
}

describe('FollowersPanel: adding a follower', () => {
  it('offers staff who are not following, not me, and not accounts that cannot follow', async () => {
    serve([ALEX])
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)

    const select = await picker()
    await waitFor(() => expect(within(select).getAllByRole('option').length).toBeGreaterThan(1))
    const names = within(select).getAllByRole('option').map((o) => o.textContent)
    expect(names).toEqual(['Select a person…', 'Bea Staff', 'Cal Colleague'])
  })

  it('adds the chosen person, then shows them once the server has agreed', async () => {
    const s = serve([ALEX])
    let release!: () => void
    const put = vi.spyOn(api, 'put').mockReturnValue(
      new Promise((res) => { release = () => res({ status: 204 }) }) as never,
    )
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    const select = await picker()
    await waitFor(() => expect(within(select).getAllByRole('option').length).toBe(3))
    const add = screen.getByRole('button', { name: 'Add' }) as HTMLButtonElement
    expect(add.disabled).toBe(true)

    await userEvent.selectOptions(select, 'u-bea')
    expect(add.disabled).toBe(false)
    await userEvent.click(add)

    expect(put).toHaveBeenCalledWith(`/tickets/${TICKET}/followers/u-bea`)
    // Not guessed for another person: nothing yet.
    expect(screen.queryByText('Bea Staff', { selector: 'li *' })).toBeNull()

    s.set([ALEX, { user_id: 'u-bea', display_name: 'Bea Staff', created_at: '2026-10-02T09:00:00Z' }])
    release()
    expect(await screen.findByText('Bea Staff', { selector: 'li *' })).toBeTruthy()
    // She is no longer offered, and the choice is cleared.
    await waitFor(() => expect(within(select).queryByRole('option', { name: 'Bea Staff' })).toBeNull())
    expect(select.value).toBe('')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it.each([
    ['follower_cannot_view_ticket', 'Bea Staff cannot see this ticket, so they cannot follow it.'],
    ['follower_not_eligible', 'Bea Staff cannot follow tickets: they must be an active staff member or administrator.'],
  ])('shows a 422 %s in the panel and changes nothing', async (code, text) => {
    serve([ALEX])
    vi.spyOn(api, 'put').mockRejectedValue(refusal(422, code, 'server words'))
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    const select = await picker()
    await waitFor(() => expect(within(select).getAllByRole('option').length).toBe(3))

    await userEvent.selectOptions(select, 'u-bea')
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))

    expect((await screen.findByRole('alert')).textContent).toBe(text)
    expect(screen.queryByText('Bea Staff', { selector: 'li *' })).toBeNull()
  })

  it('says what the server said for any other refusal', async () => {
    serve([ALEX])
    vi.spyOn(api, 'put').mockRejectedValue(refusal(403, 'session_required', 'sign in as a person'))
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    const select = await picker()
    await waitFor(() => expect(within(select).getAllByRole('option').length).toBe(3))

    await userEvent.selectOptions(select, 'u-cal')
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))

    expect((await screen.findByRole('alert')).textContent).toBe('sign in as a person')
  })

  it('cannot be double-clicked into two requests', async () => {
    serve([ALEX])
    const put = vi.spyOn(api, 'put').mockReturnValue(new Promise(() => {}) as never)
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    const select = await picker()
    await waitFor(() => expect(within(select).getAllByRole('option').length).toBe(3))
    await userEvent.selectOptions(select, 'u-cal')

    await userEvent.click(screen.getByRole('button', { name: 'Add' }))
    expect((screen.getByRole('button', { name: 'Add' }) as HTMLButtonElement).disabled).toBe(true)
    expect(put).toHaveBeenCalledTimes(1)
  })

  it('has a labelled picker and no unlabelled control (accessible)', async () => {
    serve([ALEX, ME])
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    const select = await picker()
    expect(select.id).not.toBe('')
    for (const b of await screen.findAllByRole('button')) {
      expect((b.getAttribute('aria-label') ?? b.textContent ?? '').trim()).not.toBe('')
    }
  })
})

describe('FollowersPanel: removing a follower', () => {
  it('has one remove button per follower, named for the person, and none for me', async () => {
    serve([ALEX, ME])
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)

    expect(await screen.findByRole('button', { name: 'Remove Alex Admin as a follower' })).toBeTruthy()
    // My own row has Unfollow instead.
    expect(screen.queryByRole('button', { name: 'Remove Sam Staff as a follower' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Unfollow' })).toBeTruthy()
  })

  it('removes them, and shows the list as the server then has it', async () => {
    const s = serve([ALEX, ME])
    const del = vi.spyOn(api, 'delete').mockResolvedValue({ status: 204 } as never)
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    const removeAlex = await screen.findByRole('button', { name: 'Remove Alex Admin as a follower' })
    // What the server will answer once the delete has happened.
    s.set([ME])
    await userEvent.click(removeAlex)

    expect(del).toHaveBeenCalledWith(`/tickets/${TICKET}/followers/u-alex`)
    await waitFor(() => expect(screen.queryByText('Alex Admin', { selector: 'li *' })).toBeNull())
    // And they can be added again.
    const select = await picker()
    await waitFor(() => expect(within(select).getByRole('option', { name: 'Alex Admin' })).toBeTruthy())
  })

  it('keeps them and says why when the server refuses', async () => {
    serve([ALEX, ME])
    vi.spyOn(api, 'delete').mockRejectedValue(refusal(403, 'forbidden', 'not allowed'))
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)

    await userEvent.click(await screen.findByRole('button', { name: 'Remove Alex Admin as a follower' }))

    expect((await screen.findByRole('alert')).textContent).toBe('not allowed')
    expect(screen.getByText('Alex Admin', { selector: 'li *' })).toBeTruthy()
  })
})
