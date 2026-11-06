import { describe, expect, it, vi, afterEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { AuditFeed } from './AuditFeed'

// #441: an unfollowed entry written when a ticket is reassigned has the
// reassigning actor as its actor. Said as "stopped following by <admin>" it
// names the wrong person, so it is labelled with whose follow it was.

afterEach(() => vi.restoreAllMocks())

function serve(entries: unknown[]) {
  vi.spyOn(api, 'get').mockResolvedValue({ data: entries } as never)
}

describe('follow entries in the activity feed', () => {
  it('labels a reassignment removal with the person whose follow it was', async () => {
    serve([
      {
        id: 'a1', action: 'unfollowed', actor_id: 'u-admin', actor_name: 'Ada Admin',
        created_at: '2026-10-14T10:00:00Z',
        before: { assignee_user_id: 'u-sam' }, after: { assignee_user_id: 'u-other' },
        names: { 'u-sam': { name: 'Sam Staff' }, 'u-other': { name: 'Olive Other' } },
      },
    ])
    renderWithQuery(<AuditFeed ticketId="t1" />)

    expect(await screen.findByText('Follow removed on reassignment (Sam Staff)')).toBeTruthy()
    expect(screen.queryByText('Stopped following')).toBeNull()
    // Done by the admin, and says so.
    expect(screen.getByText('Ada Admin')).toBeTruthy()
  })

  it('falls back to the id when the person no longer resolves', async () => {
    serve([
      {
        id: 'a1', action: 'unfollowed', actor_id: 'u-admin', actor_name: 'Ada Admin',
        created_at: '2026-10-14T10:00:00Z', before: { assignee_user_id: 'u-gone' }, after: { assignee_user_id: null },
      },
    ])
    renderWithQuery(<AuditFeed ticketId="t1" />)

    expect(await screen.findByText('Follow removed on reassignment (u-gone)')).toBeTruthy()
  })

  it('keeps the plain labels for an ordinary follow and unfollow', async () => {
    serve([
      { id: 'a1', action: 'followed', actor_id: 'u1', actor_name: 'Sam', created_at: '2026-10-14T09:00:00Z' },
      { id: 'a2', action: 'unfollowed', actor_id: 'u1', actor_name: 'Sam', created_at: '2026-10-14T09:30:00Z' },
    ])
    renderWithQuery(<AuditFeed ticketId="t1" />)

    expect(await screen.findByText('Started following')).toBeTruthy()
    expect(screen.getByText('Stopped following')).toBeTruthy()
  })
})
