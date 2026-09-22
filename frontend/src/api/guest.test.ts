import { describe, expect, it, vi, beforeEach } from 'vitest'

// The module builds its axios instance at import time, so the mock has to be in
// place before the import runs. vi.mock is hoisted above everything, including
// these declarations — hence vi.hoisted, which is the supported way to have a
// value exist that early.
const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('axios', () => ({
  default: { create: () => ({ get, post }) },
}))

import { addGuestReply, createGuestTicket, getGuestTicket, GuestLinkInvalid, resendGuestLink } from './guest'

// What is worth testing here is not that axios gets called. It is the three
// decisions this module makes that have security consequences: the token goes
// in a header under the Guest scheme and never in a URL, every failure to
// resolve a link becomes one indistinguishable error, and a re-request resolves
// whatever the server says so the page cannot leak what the API refuses to.

beforeEach(() => {
  get.mockReset()
  post.mockReset()
})

describe('the token never travels in a URL', () => {
  it('sends it as an Authorization header under the Guest scheme', async () => {
    get.mockResolvedValue({ data: { tracking_number: 'GHD-1', replies: [] } })

    await getGuestTicket('secret-token')

    const [path, config] = get.mock.calls[0]
    expect(path).toBe('/ticket')
    expect(path).not.toContain('secret-token')
    expect(config.headers.Authorization).toBe('Guest secret-token')
  })

  it('does the same when replying', async () => {
    post.mockResolvedValue({ data: { id: '1', body: 'hi', created_at: '', from_you: true } })

    await addGuestReply('secret-token', 'hi')

    const [path, body, config] = post.mock.calls[0]
    expect(path).toBe('/replies')
    expect(path).not.toContain('secret-token')
    expect(body).toEqual({ body: 'hi' })
    expect(config.headers.Authorization).toBe('Guest secret-token')
  })
})

describe('a link that does not work', () => {
  // The server answers 404 for expired, rotated, never-issued and closed alike,
  // deliberately. Surfacing different errors here would undo that on the page.
  it('collapses every failure into one error', async () => {
    for (const failure of [
      { response: { status: 404 } },
      { response: { status: 401 } },
      new Error('network down'),
    ]) {
      get.mockRejectedValueOnce(failure)
      await expect(getGuestTicket('t')).rejects.toBeInstanceOf(GuestLinkInvalid)
    }
  })
})

describe('creating a ticket', () => {
  it('returns the tracking number, which is all the server gives', async () => {
    post.mockResolvedValue({ data: { tracking_number: 'GHD-2026-000001' } })

    await expect(
      createGuestTicket({
        subject: 's', description: 'd', category_id: 'c',
        guest_email: 'a@b.test', guest_name: 'A',
      }),
    ).resolves.toBe('GHD-2026-000001')
  })

  it('posts to the guest endpoint, not the authenticated one', async () => {
    post.mockResolvedValue({ data: { tracking_number: 'GHD-1' } })

    await createGuestTicket({
      subject: 's', description: 'd', category_id: 'c',
      guest_email: 'a@b.test', guest_name: 'A',
    })

    expect(post.mock.calls[0][0]).toBe('/tickets')
    // The instance is created with baseURL /api/v1/guest, which is what makes
    // that path the guest route rather than the one behind RequireRole.
  })
})

describe('re-requesting a link', () => {
  // The endpoint answers 202 whatever it is given. If this rejected on failure
  // the page could show a different message, which would answer the existence
  // question the API is built to refuse.
  it('resolves when the server accepts', async () => {
    post.mockResolvedValue({ data: '' })
    await expect(resendGuestLink('GHD-1', 'a@b.test')).resolves.toBeUndefined()
  })
})
