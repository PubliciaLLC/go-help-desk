import { describe, expect, it, vi, afterEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { AuditFeed } from './AuditFeed'

// Replies and internal notes are in the feed as a sentence about who did it.
// The words themselves are not in the audit log and so not here.

afterEach(() => vi.restoreAllMocks())

function serve(entries: unknown[]) {
  vi.spyOn(api, 'get').mockResolvedValue({ data: entries } as never)
}

const at = '2026-10-20T10:00:00Z'
const diff = { reply_id: 'r-1' }

describe('reply and note entries in the activity feed', () => {
  it('says who replied publicly and who added an internal note', async () => {
    serve([
      { id: 'a1', action: 'reply_added', actor_id: 'u1', actor_name: 'Erik Test', created_at: at, after: { ...diff, internal: false } },
      { id: 'a2', action: 'note_added', actor_id: 'u1', actor_name: 'Erik Test', created_at: at, after: { ...diff, internal: true } },
    ])
    renderWithQuery(<AuditFeed ticketId="t1" />)

    expect(await screen.findByText('replied publicly')).toBeTruthy()
    expect(screen.getByText('added an internal note')).toBeTruthy()
    expect(screen.getAllByText('Erik Test')).toHaveLength(2)
  })

  it('does not print the reply id as a diff', async () => {
    serve([{ id: 'a1', action: 'reply_added', actor_id: 'u1', actor_name: 'Erik Test', created_at: at, after: { reply_id: 'r-1', internal: false } }])
    renderWithQuery(<AuditFeed ticketId="t1" />)

    await screen.findByText('replied publicly')
    expect(screen.queryByText(/r-1/)).toBeNull()
  })

  it('shows a masked requester as masked', async () => {
    serve([{ id: 'a1', action: 'reply_added', actor_id: 'u2', actor_name: 'Requester', actor_masked: true, created_at: at }])
    renderWithQuery(<AuditFeed ticketId="t1" />)

    expect(await screen.findByText('Requester (name hidden)')).toBeTruthy()
    expect(screen.getByText('replied publicly')).toBeTruthy()
  })

  it('calls a reply with no account a guest', async () => {
    serve([{ id: 'a1', action: 'reply_added', actor_id: null, created_at: at }])
    renderWithQuery(<AuditFeed ticketId="t1" />)

    expect(await screen.findByText('A guest')).toBeTruthy()
  })
})
