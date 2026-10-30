import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { FollowersPanel } from './FollowersPanel'

// #441: the Follow button and the followers list on the ticket page. Staff
// always see who is following; the button acts on the viewer only, shows the
// result at once and puts things back if the server refuses.

const TICKET = 'tkt-1'
const ME = { user_id: 'u-me', display_name: 'Sam Staff', created_at: '2026-10-01T10:00:00Z' }
const OTHER = { user_id: 'u-2', display_name: 'Alex Admin', created_at: '2026-10-01T09:00:00Z' }

function signIn() {
  useAuthStore.setState({
    user: {
      id: 'u-me', email: 'sam@example.com', display_name: 'Sam Staff', role: 'staff',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function serveFollowers(list: unknown[]) {
  return vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET}/followers`) return Promise.resolve({ data: list })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

// A request that stays open until the test lets it finish, to look at the
// screen while the server has not answered.
function held() {
  let release!: () => void
  let fail!: (e: unknown) => void
  const promise = new Promise<unknown>((res, rej) => {
    release = () => res({ status: 204 })
    fail = rej
  })
  return { promise, release, fail }
}

beforeEach(signIn)
afterEach(() => vi.restoreAllMocks())

describe('FollowersPanel', () => {
  it('lists who follows, to staff who do not', async () => {
    serveFollowers([OTHER])
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)

    expect(await screen.findByText('Alex Admin')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Follow' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Unfollow' })).toBeNull()
  })

  it('says so when nobody follows', async () => {
    serveFollowers([])
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    expect(await screen.findByText(/nobody is following/i)).toBeTruthy()
  })

  it('offers Unfollow to someone who follows, and marks them in the list', async () => {
    serveFollowers([OTHER, ME])
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)

    expect(await screen.findByRole('button', { name: 'Unfollow' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Follow' })).toBeNull()
    expect(screen.getByText('Sam Staff').closest('li')?.textContent).toMatch(/you/i)
  })

  it('follows: shows the viewer straight away, then keeps it once the server agrees', async () => {
    const get = serveFollowers([OTHER])
    const open = held()
    const put = vi.spyOn(api, 'put').mockReturnValue(open.promise as never)
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    await screen.findByText('Alex Admin')

    await userEvent.click(screen.getByRole('button', { name: 'Follow' }))

    // Before the server has answered.
    expect(put).toHaveBeenCalledWith(`/tickets/${TICKET}/follow`)
    expect(await screen.findByRole('button', { name: 'Unfollow' })).toBeTruthy()
    expect(screen.getByText(/Sam Staff/)).toBeTruthy()

    // The refetch after the answer is the truth.
    get.mockImplementation(((url: string) =>
      Promise.resolve({ data: url.endsWith('/followers') ? [OTHER, ME] : [] })) as never)
    open.release()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Unfollow' })).toBeTruthy())
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('puts the screen back and says why when following is refused', async () => {
    serveFollowers([OTHER])
    const open = held()
    vi.spyOn(api, 'put').mockReturnValue(open.promise as never)
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    await screen.findByText('Alex Admin')

    await userEvent.click(screen.getByRole('button', { name: 'Follow' }))
    await screen.findByRole('button', { name: 'Unfollow' })
    open.fail({ isAxiosError: true, response: { status: 404, data: { error: { code: 'not_found', message: 'ticket not found' } } } })

    await waitFor(() => expect(screen.getByRole('button', { name: 'Follow' })).toBeTruthy())
    expect(screen.queryByText(/Sam Staff/)).toBeNull()
    expect((await screen.findByRole('alert')).textContent).toMatch(/not found/i)
  })

  it('unfollows: removes the viewer straight away', async () => {
    serveFollowers([OTHER, ME])
    const open = held()
    const del = vi.spyOn(api, 'delete').mockReturnValue(open.promise as never)
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    await screen.findByRole('button', { name: 'Unfollow' })

    await userEvent.click(screen.getByRole('button', { name: 'Unfollow' }))

    expect(del).toHaveBeenCalledWith(`/tickets/${TICKET}/follow`)
    expect(await screen.findByRole('button', { name: 'Follow' })).toBeTruthy()
    expect(screen.queryByText(/Sam Staff/)).toBeNull()
    expect(screen.getByText('Alex Admin')).toBeTruthy()
    open.release()
  })

  it('puts the viewer back when unfollowing is refused', async () => {
    serveFollowers([ME])
    const open = held()
    vi.spyOn(api, 'delete').mockReturnValue(open.promise as never)
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    await screen.findByRole('button', { name: 'Unfollow' })

    await userEvent.click(screen.getByRole('button', { name: 'Unfollow' }))
    await screen.findByRole('button', { name: 'Follow' })
    open.fail(new Error('network down'))

    await waitFor(() => expect(screen.getByRole('button', { name: 'Unfollow' })).toBeTruthy())
    expect(screen.getByText(/Sam Staff/)).toBeTruthy()
    expect(await screen.findByRole('alert')).toBeTruthy()
  })

  it('cannot be double-clicked into two requests', async () => {
    serveFollowers([])
    const open = held()
    const put = vi.spyOn(api, 'put').mockReturnValue(open.promise as never)
    renderWithQuery(<FollowersPanel ticketId={TICKET} />)
    await screen.findByText(/nobody is following/i)

    await userEvent.click(screen.getByRole('button', { name: 'Follow' }))
    const after = await screen.findByRole('button', { name: 'Unfollow' })
    expect((after as HTMLButtonElement).disabled).toBe(true)
    expect(put).toHaveBeenCalledTimes(1)
    open.release()
  })
})
