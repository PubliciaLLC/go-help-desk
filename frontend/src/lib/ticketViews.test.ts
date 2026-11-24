import { describe, expect, it } from 'vitest'
import {
  activeFilterCount,
  parseTicketSearch,
  resolveView,
  translateAddress,
  VIEW_FILTERS,
  type FilterKey,
} from './ticketSearch'

// #676: the ticket list's views, the translation of an address made before
// them, and the filters each view offers (DESIGN.md, Ticket list views).

const ID_A = '11111111-1111-4111-8111-111111111111'
const ID_B = '22222222-2222-4222-8222-222222222222'

describe('parseTicketSearch: view and held_by', () => {
  it('reads each view and drops one that is not', () => {
    for (const view of ['me', 'groups', 'available', 'search']) {
      expect(parseTicketSearch({ view })).toEqual({ view })
    }
    expect(parseTicketSearch({ view: 'all' })).toEqual({})
    expect(parseTicketSearch({ view: 3 })).toEqual({})
  })

  it('keeps held_by only with view=groups, and only a value the server accepts', () => {
    for (const held_by of ['nobody', 'me', 'others']) {
      expect(parseTicketSearch({ view: 'groups', held_by })).toEqual({ view: 'groups', held_by })
      expect(parseTicketSearch({ view: 'me', held_by })).toEqual({ view: 'me' })
      expect(parseTicketSearch({ held_by })).toEqual({})
    }
    expect(parseTicketSearch({ view: 'groups', held_by: 'everyone' })).toEqual({ view: 'groups' })
  })

  it('still reads the old scope, which only the translation uses', () => {
    expect(parseTicketSearch({ scope: 'unassigned' })).toEqual({ scope: 'unassigned' })
  })
})

describe('resolveView: the view an address should carry', () => {
  const staff = { staff: true, admin: false, canUseAvailable: false }
  const ticked = { staff: true, admin: false, canUseAvailable: true }
  const admin = { staff: true, admin: true, canUseAvailable: true }
  const requester = { staff: false, admin: false, canUseAvailable: false }

  // The translation table of DESIGN.md, row by row.
  describe('an address without a view', () => {
    it.each([
      ['nothing', {}, 'me'],
      ['scope=mine', { scope: 'mine' }, 'me'],
      ['only toggles, sort and page (not filters)', { closed: true, sort: 'subject', page: 2, replies: false }, 'me'],
      ['text', { q: 'printer' }, 'search'],
      ['a filter (the dashboard link)', { status: [ID_A] }, 'search'],
      ['a requester filter', { requester: 'whit' }, 'search'],
      ['scope=all', { scope: 'all' }, 'search'],
      ['scope=unassigned', { scope: 'unassigned' }, 'available'],
      ['scope=mine with text', { scope: 'mine', q: 'x' }, 'search'],
      ['scope=unassigned with a filter', { scope: 'unassigned', priority: ['high'] }, 'search'],
      ['text that is only spaces', { q: '   ' }, 'me'],
    ])('%s becomes view=%s for an administrator', (_name, raw, want) => {
      expect(resolveView(raw as Record<string, unknown>, admin)).toBe(want)
    })

    it('scope=unassigned is Assigned to Me for staff the setting leaves out, and Available for ticked staff', () => {
      expect(resolveView({ scope: 'unassigned' }, staff)).toBe('me')
      expect(resolveView({ scope: 'unassigned' }, ticked)).toBe('available')
    })

    it('scope=all is Search for staff too (Search is every ticket they can see)', () => {
      expect(resolveView({ scope: 'all' }, staff)).toBe('search')
    })
  })

  describe('an address with a view', () => {
    it('keeps it, whatever else the address holds', () => {
      expect(resolveView({ view: 'groups', q: 'x', scope: 'all' }, staff)).toBe('groups')
      expect(resolveView({ view: 'me', priority: ['high'] }, staff)).toBe('me')
      expect(resolveView({ view: 'search' }, staff)).toBe('search')
    })

    it('view=available needs the setting (or an administrator), and is Assigned to Me without it', () => {
      expect(resolveView({ view: 'available' }, staff)).toBe('me')
      expect(resolveView({ view: 'available' }, ticked)).toBe('available')
      expect(resolveView({ view: 'available' }, admin)).toBe('available')
    })
  })

  it('a requester has no views', () => {
    for (const raw of [{}, { view: 'groups' }, { scope: 'all' }, { q: 'x' }, { view: 'available' }]) {
      expect(resolveView(raw, requester)).toBeUndefined()
    }
  })
})

describe('translateAddress: the address that replaces the one typed or bookmarked', () => {
  const staff = { staff: true, admin: false, canUseAvailable: false }
  const admin = { staff: true, admin: true, canUseAvailable: true }
  const requester = { staff: false, admin: false, canUseAvailable: false }

  it('writes the view, drops scope, and keeps everything else', () => {
    expect(translateAddress({ scope: 'unassigned' }, admin)).toEqual({ view: 'available' })
    expect(translateAddress({ scope: 'all', q: 'x', sort: 'subject', order: 'asc', page: 2 }, admin)).toEqual({
      view: 'search',
      q: 'x',
      sort: 'subject',
      order: 'asc',
      page: 2,
    })
    expect(translateAddress({ status: ID_A }, staff)).toEqual({ view: 'search', status: [ID_A] })
    expect(translateAddress({}, staff)).toEqual({ view: 'me' })
  })

  it('keeps held_by with view=groups and drops it elsewhere', () => {
    expect(translateAddress({ view: 'groups', held_by: 'me' }, staff)).toEqual({ view: 'groups', held_by: 'me' })
    expect(translateAddress({ view: 'available', held_by: 'me' }, admin)).toEqual({ view: 'available' })
  })

  it('a view the viewer may not use falls back to Assigned to Me', () => {
    expect(translateAddress({ view: 'available', q: 'x' }, staff)).toEqual({ view: 'me', q: 'x' })
    expect(translateAddress({ view: 'available', held_by: 'me' }, staff)).toEqual({ view: 'me' })
  })

  it('is its own fixed point', () => {
    for (const raw of [{}, { scope: 'all' }, { view: 'groups', held_by: 'others', q: 'x' }, { scope: 'unassigned' }]) {
      const once = translateAddress(raw, admin)
      expect(translateAddress(once as Record<string, unknown>, admin)).toEqual(once)
    }
  })

  it('a requester loses view, scope and held_by and keeps their search', () => {
    expect(translateAddress({ view: 'groups', held_by: 'me', scope: 'all', q: 'x', priority: ['high'] }, requester)).toEqual({
      q: 'x',
      priority: ['high'],
    })
  })
})

describe('VIEW_FILTERS: the filters each view offers', () => {
  const ALL: FilterKey[] = ['status', 'priority', 'category', 'type', 'item', 'assignee', 'group', 'requester', 'created', 'updated', 'channel', 'sla', 'awaiting']
  const sorted = (a: readonly FilterKey[]) => [...a].sort()

  it('Assigned to Me leaves out Assignee, which is always me', () => {
    expect(sorted(VIEW_FILTERS.me)).toEqual(sorted(ALL.filter(k => k !== 'assignee')))
  })

  it('Available leaves out Assignee, Group, Requester and Awaiting my reply', () => {
    expect(sorted(VIEW_FILTERS.available)).toEqual(sorted(ALL.filter(k => !['assignee', 'group', 'requester', 'awaiting'].includes(k))))
  })

  it("My Group's Tickets offers every filter; Search every filter but Awaiting my reply", () => {
    expect(sorted(VIEW_FILTERS.groups)).toEqual(sorted(ALL))
    expect(sorted(VIEW_FILTERS.search)).toEqual(sorted(ALL.filter(k => k !== 'awaiting')))
  })

  it('a filter a view does not offer is not counted either', () => {
    const link = { assignee: ID_A, group: ID_B, requester: 'whit', priority: ['high' as const] }
    expect(activeFilterCount(link, true, VIEW_FILTERS.available)).toBe(1)
    expect(activeFilterCount(link, true, VIEW_FILTERS.me)).toBe(3)
    expect(activeFilterCount(link, true, VIEW_FILTERS.search)).toBe(4)
  })
})
