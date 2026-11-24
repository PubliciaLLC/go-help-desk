import { describe, expect, it } from 'vitest'
import { QUEUE_CELLS, addressSearch, knownCells } from './dashboard'
import { PAGE_SIZE, parseTicketSearch, toApiParams } from './ticketSearch'
import type { TicketListParams, Viewer } from './ticketSearch'
import type { QueueKey } from '@/api/types'

// DESIGN.md, Dashboard, The queue strip: each cell links to the list that shows
// exactly the tickets it counts, so the address, read the way the ticket list
// reads it, must send the parameters the table documents (GET /tickets with ...).
const STAFF: Viewer = { staff: true, admin: false, canUseAvailable: false, slaOn: true }
const ADMIN: Viewer = { staff: true, admin: true, canUseAvailable: true, slaOn: true }
const REQUESTER: Viewer = { staff: false, admin: false, canUseAvailable: false, slaOn: false }

const BOTH = [['staff', STAFF], ['admin', ADMIN]] as const

const page = { limit: PAGE_SIZE, offset: 0 }

const cases: { key: QueueKey; label: string; href: string; viewers: readonly (readonly [string, Viewer])[]; want: TicketListParams }[] = [
  {
    key: 'assigned_to_me',
    label: 'Assigned to me',
    href: '/tickets?view=me',
    viewers: BOTH,
    want: { ...page, scope: 'assigned_to_me', include_closed: false },
  },
  {
    key: 'my_groups',
    label: 'My groups',
    href: '/tickets?view=groups',
    viewers: BOTH,
    want: { ...page, scope: 'my_groups', include_closed: false },
  },
  {
    key: 'sla_breached',
    label: 'Breached',
    href: '/tickets?view=me&sla=breached',
    viewers: BOTH,
    want: { ...page, scope: 'assigned_to_me', sla: 'breached', include_closed: false },
  },
  {
    key: 'sla_at_risk',
    label: 'At risk',
    href: '/tickets?view=me&sla=at_risk',
    viewers: BOTH,
    want: { ...page, scope: 'assigned_to_me', sla: 'at_risk', include_closed: false },
  },
  {
    key: 'awaiting_my_reply',
    label: 'Awaiting my reply',
    href: '/tickets?view=me&awaiting_reply=true',
    viewers: BOTH,
    want: { ...page, scope: 'assigned_to_me', awaiting_reply: true, include_closed: false },
  },
  {
    key: 'my_tickets',
    label: 'Your tickets',
    href: '/tickets',
    viewers: [['requester', REQUESTER]],
    want: { ...page, scope: 'mine', include_closed: false },
  },
]

describe('QUEUE_CELLS', () => {
  it('has exactly the six keys of the table, each with a label', () => {
    expect(Object.keys(QUEUE_CELLS).sort()).toEqual(cases.map((c) => c.key).sort())
    for (const c of cases) expect(QUEUE_CELLS[c.key].label).toBe(c.label)
  })

  describe.each(cases)('$key', ({ key, href, viewers, want }) => {
    it('links where the table says', () => {
      expect(QUEUE_CELLS[key].href).toBe(href)
    })

    it.each(viewers)('the address sends the documented request for %s', (_who, viewer) => {
      expect(toApiParams(parseTicketSearch(addressSearch(QUEUE_CELLS[key].href)), viewer)).toEqual(want)
    })
  })
})

describe('addressSearch', () => {
  it('reads the query string into the object the router parses', () => {
    expect(addressSearch('/tickets?view=me&sla=breached')).toEqual({ view: 'me', sla: 'breached' })
  })

  it('is empty for an address with no query', () => {
    expect(addressSearch('/tickets')).toEqual({})
  })
})

describe('knownCells', () => {
  it('keeps the server order and attaches the label and address', () => {
    const got = knownCells([
      { key: 'my_groups', count: 11 },
      { key: 'assigned_to_me', count: 0 },
    ])
    expect(got).toEqual([
      { key: 'my_groups', count: 11, label: 'My groups', href: '/tickets?view=groups' },
      { key: 'assigned_to_me', count: 0, label: 'Assigned to me', href: '/tickets?view=me' },
    ])
  })

  it('ignores a key it does not know', () => {
    const got = knownCells([
      { key: 'assigned_to_me', count: 4 },
      { key: 'from_the_future', count: 9 },
    ])
    expect(got.map((c) => c.key)).toEqual(['assigned_to_me'])
  })

  it('is empty for no queues', () => {
    expect(knownCells([])).toEqual([])
  })
})
