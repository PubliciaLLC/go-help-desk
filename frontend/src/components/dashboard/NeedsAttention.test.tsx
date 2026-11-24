import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import type { AttentionItem, AttentionReason, Status, Ticket } from '@/api/types'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ to, params, children, ...rest }: { to: string; params: Record<string, string>; children: React.ReactNode }) => (
    <a href={to.replace('$id', params.id)} {...rest}>
      {children}
    </a>
  ),
}))

import { NeedsAttention } from './NeedsAttention'

const H = 3600 * 1000
const ago = (ms: number) => new Date(Date.now() - ms).toISOString()

const STATUSES: Status[] = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 },
]

function item(reason: AttentionReason, over: Partial<Ticket> = {}, since = ago(72 * H)): AttentionItem {
  return {
    reason,
    since,
    ticket: {
      id: `tk-${reason}`,
      tracking_number: `GHD-2026-${reason}`,
      subject: `Subject of ${reason}`,
      description: '',
      description_format: 'plain',
      category_id: 'cat-1',
      priority: 'medium',
      status_id: 'st-new',
      created_at: ago(500 * H),
      ...over,
    } as Ticket,
  }
}

describe('NeedsAttention rows', () => {
  it('is a list of links to the tickets, never a table', () => {
    const { container } = render(
      <NeedsAttention items={[item('breached'), item('at_risk')]} statuses={STATUSES} />,
    )
    expect(container.querySelector('table')).toBeNull()
    const rows = screen.getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByRole('link').getAttribute('href')).toBe('/tickets/tk-breached')
    expect(within(rows[1]).getByRole('link').getAttribute('href')).toBe('/tickets/tk-at_risk')
  })

  it('shows the tracking number and the subject', () => {
    render(<NeedsAttention items={[item('breached')]} statuses={STATUSES} />)
    expect(screen.getByText('GHD-2026-breached')).toBeTruthy()
    expect(screen.getByText('Subject of breached')).toBeTruthy()
  })

  it('wraps a long subject rather than cutting it', () => {
    const long = 'S'.repeat(300)
    render(<NeedsAttention items={[item('breached', { subject: long })]} statuses={STATUSES} />)
    const el = screen.getByText(long)
    expect(el.className).toContain('wrap-anywhere')
    expect(el.className).not.toContain('truncate')
  })

  const badges = [
    { reason: 'breached' as const, text: 'Breached', cls: 'red' },
    { reason: 'at_risk' as const, text: 'At risk', cls: 'amber' },
    { reason: 'unassigned' as const, text: 'Unassigned', cls: 'gray' },
  ]
  it.each(badges)('labels a $reason row $text', ({ reason, text, cls }) => {
    render(<NeedsAttention items={[item(reason)]} statuses={STATUSES} />)
    const badge = screen.getByText(text)
    expect(badge.className).toContain(cls)
    expect(screen.queryByTestId('reopened-badge')).toBeNull()
  })

  it('labels a reopened row with the reopened badge, and no other', () => {
    render(<NeedsAttention items={[item('reopened', { reopen_count: 1 })]} statuses={STATUSES} />)
    expect(screen.getByTestId('reopened-badge').textContent).toBe('Reopened')
    expect(screen.queryByText('Breached')).toBeNull()
  })

  it('shows the reopened badge even before the statuses have loaded', () => {
    render(<NeedsAttention items={[item('reopened', { reopen_count: 2 })]} />)
    expect(screen.getByTestId('reopened-badge')).toBeTruthy()
  })
})

describe('NeedsAttention age', () => {
  it('says opened, from since, for the reasons that are about the ticket', () => {
    render(<NeedsAttention items={[item('breached', {}, ago(3 * 24 * H))]} statuses={STATUSES} />)
    expect(screen.getByText('opened 3 days ago')).toBeTruthy()
  })

  it('says reopened, from since and not from created_at, on a reopened row', () => {
    // created 500 hours ago (20 days), reopened 2 hours ago.
    render(<NeedsAttention items={[item('reopened', { reopen_count: 1 }, ago(2 * H))]} statuses={STATUSES} />)
    expect(screen.getByText('reopened 2 hours ago')).toBeTruthy()
    expect(screen.queryByText(/20 days/)).toBeNull()
  })

  it('puts the full local time in the title', () => {
    const since = '2026-11-21T09:14:03Z'
    render(<NeedsAttention items={[item('breached', {}, since)]} statuses={STATUSES} />)
    const age = screen.getByText(/^opened /)
    expect(age.getAttribute('title')).toBe(new Date(since).toLocaleString())
  })
})

describe('NeedsAttention empty', () => {
  it('says nothing needs attention', () => {
    render(<NeedsAttention items={[]} statuses={STATUSES} />)
    expect(screen.getByText('Nothing needs attention right now.')).toBeTruthy()
    expect(screen.queryByRole('list')).toBeNull()
  })
})
