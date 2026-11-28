import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import type { QueueCount } from '@/api/types'

vi.mock('@tanstack/react-router', () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, search, children, ...rest }: any) => (
    <a href={search && Object.keys(search).length ? `${to}?${new URLSearchParams(search).toString()}` : to} {...rest}>
      {children}
    </a>
  ),
}))

import { QueueStrip } from './QueueStrip'

const STAFF_QUEUES: QueueCount[] = [
  { key: 'assigned_to_me', count: 4 },
  { key: 'my_groups', count: 11 },
  { key: 'sla_breached', count: 1 },
  { key: 'sla_at_risk', count: 2 },
  { key: 'awaiting_my_reply', count: 3 },
]

const count = (link: HTMLElement) => within(link).getByTestId('queue-count')

describe('QueueStrip for staff and administrators', () => {
  it('is a navigation named Your queue holding one link per cell, in the server order', () => {
    render(<QueueStrip queues={STAFF_QUEUES} requester={false} />)
    const nav = screen.getByRole('navigation', { name: 'Your queue' })
    const links = within(nav).getAllByRole('link')
    expect(links.map((a) => a.getAttribute('href'))).toEqual([
      '/tickets?view=me',
      '/tickets?view=groups',
      '/tickets?view=me&sla=breached',
      '/tickets?view=me&sla=at_risk',
      '/tickets?view=me&awaiting_reply=true',
    ])
  })

  it('reads each cell as one phrase', () => {
    render(<QueueStrip queues={STAFF_QUEUES} requester={false} />)
    for (const name of ['4 Assigned to me', '11 My groups', '1 Breached', '2 At risk', '3 Awaiting my reply']) {
      expect(screen.getByRole('link', { name })).toBeTruthy()
    }
  })

  it('is headed Your queue', () => {
    render(<QueueStrip queues={STAFF_QUEUES} requester={false} />)
    expect(screen.getByRole('heading', { name: 'Your queue' })).toBeTruthy()
  })

  it('shows a zero as 0', () => {
    render(<QueueStrip queues={[{ key: 'my_groups', count: 0 }]} requester={false} />)
    expect(screen.getByRole('link', { name: '0 My groups' })).toBeTruthy()
  })

  it('leaves out a cell the server did not send (SLA tracking off)', () => {
    render(
      <QueueStrip
        queues={[
          { key: 'assigned_to_me', count: 4 },
          { key: 'my_groups', count: 11 },
          { key: 'awaiting_my_reply', count: 3 },
        ]}
        requester={false}
      />,
    )
    expect(screen.queryByRole('link', { name: /Breached/ })).toBeNull()
    expect(screen.queryByRole('link', { name: /At risk/ })).toBeNull()
    expect(screen.getAllByRole('link')).toHaveLength(3)
  })

  it('ignores a key it does not know', () => {
    render(<QueueStrip queues={[...STAFF_QUEUES, { key: 'from_the_future', count: 9 }]} requester={false} />)
    expect(screen.getAllByRole('link')).toHaveLength(5)
  })

  it('lays out two, three and five columns at base, sm and lg', () => {
    render(<QueueStrip queues={STAFF_QUEUES} requester={false} />)
    const list = screen.getByRole('list')
    for (const c of ['grid', 'grid-cols-2', 'sm:grid-cols-3', 'lg:grid-cols-5']) {
      expect(list.className).toContain(c)
    }
  })

  it('makes every cell at least 44px tall on a coarse pointer', () => {
    render(<QueueStrip queues={STAFF_QUEUES} requester={false} />)
    for (const a of screen.getAllByRole('link')) expect(a.className).toContain('pointer-coarse:min-h-11')
  })
})

describe('QueueStrip SLA colours', () => {
  const cases = [
    { name: 'Breached above zero is red', key: 'sla_breached', n: 2, want: 'text-danger-fg' },
    { name: 'At risk above zero is amber', key: 'sla_at_risk', n: 2, want: 'text-warning-fg' },
    { name: 'Breached at zero is grey', key: 'sla_breached', n: 0, want: 'text-fg-faint', not: 'text-danger-fg' },
    { name: 'At risk at zero is grey', key: 'sla_at_risk', n: 0, want: 'text-fg-faint', not: 'text-warning-fg' },
    { name: 'a non-SLA cell above zero is never coloured', key: 'assigned_to_me', n: 7, want: 'text-fg', not: 'text-danger-fg' },
  ]
  it.each(cases)('$name', ({ key, n, want, not }) => {
    render(<QueueStrip queues={[{ key, count: n }]} requester={false} />)
    const el = count(screen.getByRole('link'))
    expect(el.className).toContain(want)
    if (not) expect(el.className).not.toContain(not)
  })
})

describe('QueueStrip for a requester', () => {
  it('is a navigation named Your tickets with the one cell, linking to the plain list', () => {
    render(<QueueStrip queues={[{ key: 'my_tickets', count: 6 }]} requester />)
    const nav = screen.getByRole('navigation', { name: 'Your tickets' })
    const links = within(nav).getAllByRole('link')
    expect(links).toHaveLength(1)
    expect(links[0].getAttribute('href')).toBe('/tickets')
    expect(screen.getByRole('link', { name: '6 Your tickets' })).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Your tickets' })).toBeTruthy()
  })
})
