import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import {
  activeFilterCount,
  clearAll,
  clearFilter,
  dayEnd,
  dayStart,
  effectiveSort,
  hasNarrowing,
  nextSort,
  parseTicketSearch,
  toApiParams,
  toUrlSearch,
  VIEW_FILTERS,
  type TicketSearch,
} from './ticketSearch'

const ID_A = '11111111-1111-4111-8111-111111111111'
const ID_B = '22222222-2222-4222-8222-222222222222'

describe('parseTicketSearch: URL to state', () => {
  it('an empty address is no filters at all', () => {
    expect(parseTicketSearch({})).toEqual({})
  })

  it('the old status=<id> link reads as a one-status filter', () => {
    expect(parseTicketSearch({ status: ID_A })).toEqual({ status: [ID_A] })
  })

  it('the old reporter=<id> link keeps working', () => {
    expect(parseTicketSearch({ reporter: ID_A })).toEqual({ reporter: ID_A })
  })

  it('status and priority may be lists, and a repeated value is kept once', () => {
    expect(parseTicketSearch({ status: [ID_A, ID_B, ID_A], priority: ['high', 'low'] })).toEqual({
      status: [ID_A, ID_B],
      priority: ['high', 'low'],
    })
  })

  // These go into request paths and query strings; only an id may.
  it.each(['category', 'type', 'item', 'assignee', 'group', 'reporter'] as const)(
    '%s keeps an id and drops anything else',
    field => {
      expect(parseTicketSearch({ [field]: ID_A })).toEqual({ [field]: ID_A })
      for (const bad of ['../admin/users', 'c1', '', '123', ID_A + '/x', '..%2Fadmin', ` ${ID_A}x`]) {
        expect(parseTicketSearch({ [field]: bad })).toEqual({})
      }
    },
  )

  it('status drops what is not an id and keeps the rest', () => {
    expect(parseTicketSearch({ status: ['../admin/users', ID_A, 'open'] })).toEqual({ status: [ID_A] })
    expect(parseTicketSearch({ status: '../admin/users' })).toEqual({})
  })

  // An address with both is one the form cannot make. The chip names the
  // account, so the account is what the page keeps and sends.
  it('an account wins over free-text requester, so the chip and the request agree', () => {
    expect(parseTicketSearch({ reporter: ID_A, requester: 'whit' })).toEqual({ reporter: ID_A })
    expect(parseTicketSearch({ requester: 'whit' })).toEqual({ requester: 'whit' })
  })

  // The server holds requester to 2-254 characters and status to 20 values.
  it('drops a requester the server would refuse for its length', () => {
    expect(parseTicketSearch({ requester: 'w' })).toEqual({})
    expect(parseTicketSearch({ requester: 'wh' })).toEqual({ requester: 'wh' })
    expect(parseTicketSearch({ requester: 'x'.repeat(254) })).toEqual({ requester: 'x'.repeat(254) })
    expect(parseTicketSearch({ requester: 'x'.repeat(255) })).toEqual({})
  })

  it('keeps at most 20 statuses', () => {
    const many = Array.from({ length: 25 }, (_, i) => `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`)
    expect(parseTicketSearch({ status: many }).status).toEqual(many.slice(0, 20))
  })

  it('drops values the server would answer 400 for', () => {
    expect(
      parseTicketSearch({
        priority: ['urgent', 'high'],
        channel: ['fax', 'phone'],
        sla: 'late',
        sort: 'relevance',
        order: 'sideways',
        scope: 'visible',
        created_from: 'yesterday',
        updated_to: '2026-13-45x',
        page: 'abc',
      }),
    ).toEqual({ priority: ['high'], channel: ['phone'] })
  })

  // TanStack Router's default parser runs JSON.parse on every value, so a
  // search for "2026" or "true" arrives as a number or a boolean.
  it('a search for a number or the word true still arrives as text', () => {
    expect(parseTicketSearch({ q: 2026 })).toEqual({ q: '2026' })
    expect(parseTicketSearch({ q: true })).toEqual({ q: 'true' })
  })

  it('replies is stored only when it is off (the page default is on)', () => {
    expect(parseTicketSearch({ replies: false })).toEqual({ replies: false })
    expect(parseTicketSearch({ replies: 'false' })).toEqual({ replies: false })
    expect(parseTicketSearch({ replies: true })).toEqual({})
  })

  it('closed is stored only when it is on', () => {
    expect(parseTicketSearch({ closed: true })).toEqual({ closed: true })
    expect(parseTicketSearch({ closed: false })).toEqual({})
  })

  // awaiting_reply=true is on; the page reads it as text or as a boolean, as
  // every value arrives. A value that is not "on" is not stored.
  it('awaiting_reply is stored only when on, as the address spells it', () => {
    expect(parseTicketSearch({ awaiting_reply: 'true' })).toEqual({ awaiting_reply: true })
    expect(parseTicketSearch({ awaiting_reply: true })).toEqual({ awaiting_reply: true })
    expect(parseTicketSearch({ awaiting_reply: false })).toEqual({})
    expect(parseTicketSearch({ awaiting_reply: 'yes' })).toEqual({})
  })

  it('page counts from 1 and is stored from 2', () => {
    expect(parseTicketSearch({ page: 3 })).toEqual({ page: 3 })
    expect(parseTicketSearch({ page: 1 })).toEqual({})
    expect(parseTicketSearch({ page: 0 })).toEqual({})
  })

  it('keeps a sort, and an order', () => {
    expect(parseTicketSearch({ sort: 'subject', order: 'desc' })).toEqual({ sort: 'subject', order: 'desc' })
  })

  it('an order with no sort is dropped (the server would ignore it)', () => {
    expect(parseTicketSearch({ order: 'desc' })).toEqual({})
  })
})

describe('toUrlSearch: state to URL', () => {
  it('leaves out everything that is empty, so the address stays short', () => {
    const s: TicketSearch = { q: '', status: [], priority: undefined, category: '' }
    expect(toUrlSearch(s)).toEqual({})
  })

  it('round-trips through the parser', () => {
    const s: TicketSearch = {
      q: 'fuser',
      replies: false,
      status: [ID_A],
      priority: ['high'],
      created_from: '2026-10-01',
      awaiting_reply: true,
      sort: 'created',
      order: 'asc',
      page: 2,
    }
    expect(parseTicketSearch({ ...toUrlSearch(s) })).toEqual(s)
  })
})

describe('dates are the viewer\'s own', () => {
  // stubEnv rather than process.env: the app's tsconfig has no Node types.
  beforeAll(() => {
    vi.stubEnv('TZ', 'America/New_York')
  })
  afterAll(() => {
    vi.unstubAllEnvs()
  })

  it('a day starts at local midnight and ends exclusive at the next one', () => {
    // 2026-10-01 is still daylight time in New York: UTC-4.
    expect(dayStart('2026-10-01')).toBe('2026-10-01T04:00:00.000Z')
    expect(dayEnd('2026-10-01')).toBe('2026-10-02T04:00:00.000Z')
  })

  it('the end date is included even across the autumn clock change', () => {
    // 2026-11-01: clocks go back at 02:00, so the day is 25 hours long.
    expect(dayStart('2026-11-01')).toBe('2026-11-01T04:00:00.000Z')
    expect(dayEnd('2026-11-01')).toBe('2026-11-02T05:00:00.000Z')
  })

  it('and across the spring one, when the day is 23 hours long', () => {
    expect(dayStart('2027-03-14')).toBe('2027-03-14T05:00:00.000Z')
    expect(dayEnd('2027-03-14')).toBe('2027-03-15T04:00:00.000Z')
  })
})

describe('toApiParams', () => {
  const staff = { staff: true, admin: false, canUseAvailable: false, slaOn: true }
  const admin = { staff: true, admin: true, canUseAvailable: true, slaOn: true }
  const requester = { staff: false, admin: false, canUseAvailable: false, slaOn: false }

  it('names every filter the way the API does', () => {
    const p = toApiParams(
      {
        q: 'fuser',
        status: [ID_A, ID_B],
        priority: ['high'],
        category: 'c1',
        type: 't1',
        item: 'i1',
        assignee: 'u1',
        group: 'g1',
        requester: 'whit',
        channel: ['web', 'phone'],
        sla: 'at_risk',
      },
      staff,
    )
    expect(p).toMatchObject({
      q: 'fuser',
      status_id: [ID_A, ID_B],
      priority: ['high'],
      category_id: 'c1',
      type_id: 't1',
      item_id: 'i1',
      assignee_user_id: 'u1',
      assignee_group_id: 'g1',
      requester: 'whit',
      source_channel: ['web', 'phone'],
      sla: 'at_risk',
    })
  })

  it('the page searches replies unless told not to; the API default is off', () => {
    expect(toApiParams({ q: 'x' }, staff).search_replies).toBe(true)
    expect(toApiParams({ q: 'x', replies: false }, staff).search_replies).toBeUndefined()
  })

  it('sends no reply search without text to search', () => {
    expect(toApiParams({}, staff).search_replies).toBeUndefined()
  })

  it('leaves closed tickets out unless the toggle is on', () => {
    expect(toApiParams({}, staff).include_closed).toBe(false)
    expect(toApiParams({ closed: true }, staff).include_closed).toBeUndefined()
  })

  it('sends no sort until a header is clicked, so a text search stays ranked', () => {
    const p = toApiParams({ q: 'fuser' }, staff)
    expect(p.sort).toBeUndefined()
    expect(p.order).toBeUndefined()
  })

  it('always sends an explicit order with a sort', () => {
    expect(toApiParams({ sort: 'created', order: 'asc' }, staff)).toMatchObject({ sort: 'created', order: 'asc' })
    expect(toApiParams({ sort: 'created' }, staff)).toMatchObject({ sort: 'created', order: 'desc' })
    expect(toApiParams({ sort: 'priority' }, staff)).toMatchObject({ sort: 'priority', order: 'asc' })
  })

  it('turns dates into instants, the end date being included', () => {
    const p = toApiParams({ created_from: '2026-10-01', created_to: '2026-10-31', updated_from: '2026-11-03' }, staff)
    expect(p.created_after).toBe(dayStart('2026-10-01'))
    expect(p.created_before).toBe(dayEnd('2026-10-31'))
    expect(p.updated_after).toBe(dayStart('2026-11-03'))
    expect(p.updated_before).toBeUndefined()
  })

  it('pages: the URL counts from 1, the API from an offset', () => {
    expect(toApiParams({}, staff)).toMatchObject({ limit: 50, offset: 0 })
    expect(toApiParams({ page: 3 }, staff)).toMatchObject({ limit: 50, offset: 100 })
  })

  // The page always writes a view, and the API scope is derived from it
  // (DESIGN.md, Ticket list views). Text and filters narrow within the view.
  it.each([
    ['me', 'assigned_to_me'],
    ['groups', 'my_groups'],
    ['available', 'unassigned'],
    ['search', 'visible'],
  ] as const)('the %s view sends scope=%s', (view, scope) => {
    expect(toApiParams({ view }, admin).scope).toBe(scope)
    // Text and filters do not change it: a queue stays that queue.
    expect(toApiParams({ view, q: 'x', priority: ['high'] }, admin).scope).toBe(scope)
  })

  it('an address with no view is read as the view the translation would give it', () => {
    expect(toApiParams({}, staff).scope).toBe('assigned_to_me')
    expect(toApiParams({ q: 'x' }, staff).scope).toBe('visible')
    expect(toApiParams({ scope: 'unassigned' }, admin).scope).toBe('unassigned')
  })

  it('the old Only <queue> widening is gone: text in a queue stays in the queue', () => {
    expect(toApiParams({ view: 'me', q: 'x' }, staff).scope).toBe('assigned_to_me')
    expect(toApiParams({ view: 'groups', sla: 'breached' }, staff).scope).toBe('my_groups')
  })

  it('the closed toggle and the sort do not change the scope', () => {
    expect(toApiParams({ view: 'me', closed: true, sort: 'subject' }, staff).scope).toBe('assigned_to_me')
  })

  // REVIEW AMENDMENT: today's page sends no scope when a reporter, a requester
  // or a group is set, and the server reads an absent scope as mine, which would
  // widen the list. The scope is always sent.
  it('always sends the scope, even when a group, a requester or an account is named', () => {
    expect(toApiParams({ view: 'groups', group: ID_A }, staff)).toMatchObject({ scope: 'my_groups', assignee_group_id: ID_A })
    expect(toApiParams({ view: 'me', requester: 'whit' }, staff)).toMatchObject({ scope: 'assigned_to_me', requester: 'whit' })
    expect(toApiParams({ view: 'me', reporter: ID_A }, staff)).toMatchObject({ scope: 'assigned_to_me', reporter_id: ID_A })
    expect(toApiParams({ view: 'search', reporter: ID_A }, staff).scope).toBe('visible')
  })

  // awaiting_reply is offered on Assigned to Me and My Group's Tickets only
  // (DESIGN.md, Dashboard). Available and Search do not send it even when the
  // address carries it.
  it.each([
    ['me', true],
    ['groups', true],
    ['available', false],
    ['search', false],
  ] as const)('awaiting_reply on the %s view is sent: %s', (view, sent) => {
    const p = toApiParams({ view, awaiting_reply: true }, admin)
    if (sent) expect(p.awaiting_reply).toBe(true)
    else expect(p).not.toHaveProperty('awaiting_reply')
  })

  it('awaiting_reply is not sent when it is not set', () => {
    expect(toApiParams({ view: 'me' }, staff)).not.toHaveProperty('awaiting_reply')
  })

  it('held_by is sent on My Group\'s Tickets and nowhere else', () => {
    expect(toApiParams({ view: 'groups', held_by: 'nobody' }, staff)).toMatchObject({ scope: 'my_groups', held_by: 'nobody' })
    expect(toApiParams({ view: 'groups', held_by: 'me' }, staff).held_by).toBe('me')
    expect(toApiParams({ view: 'groups', held_by: 'others' }, staff).held_by).toBe('others')
    expect(toApiParams({ view: 'groups' }, staff)).not.toHaveProperty('held_by')
    for (const view of ['me', 'available', 'search'] as const) {
      expect(toApiParams({ view, held_by: 'me' }, admin), view).not.toHaveProperty('held_by')
    }
  })

  it('a view the viewer may not use is Assigned to Me', () => {
    expect(toApiParams({ view: 'available' }, staff).scope).toBe('assigned_to_me')
    expect(toApiParams({ view: 'available' }, { ...staff, canUseAvailable: true }).scope).toBe('unassigned')
  })

  it('drops the filters a view does not offer, even if the address carries them', () => {
    const all = { assignee: 'u1', group: 'g1', reporter: ID_A, requester: 'whit', priority: ['high' as const] }
    const me = toApiParams({ view: 'me', ...all }, staff)
    expect(me).not.toHaveProperty('assignee_user_id')
    expect(me).toMatchObject({ assignee_group_id: 'g1', reporter_id: ID_A, priority: ['high'] })

    const available = toApiParams({ view: 'available', ...all }, admin)
    for (const k of ['assignee_user_id', 'assignee_group_id', 'reporter_id', 'requester']) {
      expect(available, k).not.toHaveProperty(k)
    }
    expect(available).toMatchObject({ scope: 'unassigned', priority: ['high'] })

    for (const view of ['groups', 'search'] as const) {
      expect(toApiParams({ view, ...all }, staff), view).toMatchObject({
        assignee_user_id: 'u1',
        assignee_group_id: 'g1',
        reporter_id: ID_A,
      })
    }
  })

  describe('for a requester', () => {
    it('drops every filter and sort the server would refuse with 403', () => {
      const p = toApiParams(
        {
          priority: ['high'],
          reporter: ID_A,
          requester: 'whit',
          assignee: 'u1',
          group: 'g1',
          channel: ['web'],
          sla: 'breached',
          updated_from: '2026-11-03',
          updated_to: '2026-11-04',
          sort: 'updated',
        },
        requester,
      )
      expect(p).toMatchObject({ priority: ['high'] })
      for (const k of ['reporter_id', 'requester', 'assignee_user_id', 'assignee_group_id', 'source_channel', 'sla', 'updated_after', 'updated_before', 'sort', 'order']) {
        expect(p, k).not.toHaveProperty(k)
      }
    })

    it('may sort by the five columns they are sent', () => {
      for (const sort of ['number', 'subject', 'status', 'priority', 'created'] as const) {
        expect(toApiParams({ sort }, requester).sort).toBe(sort)
      }
      expect(toApiParams({ sort: 'sla' }, requester).sort).toBeUndefined()
    })

    it('always asks for the queue the server expects of them', () => {
      expect(toApiParams({ q: 'x' }, requester).scope).toBe('mine')
    })

    it('never sends awaiting_reply, which the server refuses with 403 to a requester', () => {
      expect(toApiParams({ awaiting_reply: true }, requester)).not.toHaveProperty('awaiting_reply')
    })
  })
})

describe('toApiParams while SLA tracking is not known to be on', () => {
  const off = { staff: true, admin: false, canUseAvailable: false, slaOn: false }

  it('drops the SLA filter and the SLA sort, which the server would answer 400', () => {
    const p = toApiParams({ sla: 'breached', sort: 'sla', order: 'desc', priority: ['high'] }, off)
    expect(p).toMatchObject({ priority: ['high'] })
    for (const k of ['sla', 'sort', 'order']) expect(p, k).not.toHaveProperty(k)
  })

  it('keeps another sort, and everything when SLA is on', () => {
    expect(toApiParams({ sort: 'subject', order: 'asc' }, off)).toMatchObject({ sort: 'subject', order: 'asc' })
    expect(toApiParams({ sla: 'breached', sort: 'sla', order: 'desc' }, { ...off, slaOn: true })).toMatchObject({
      sla: 'breached',
      sort: 'sla',
      order: 'desc',
    })
  })
})

describe('VIEW_FILTERS: awaiting_reply', () => {
  it.each([
    ['me', true],
    ['groups', true],
    ['available', false],
    ['search', false],
  ] as const)('the %s view offers it: %s', (view, offered) => {
    expect(VIEW_FILTERS[view].includes('awaiting')).toBe(offered)
  })
})

describe('hasNarrowing / activeFilterCount', () => {
  it('a requester counts only the filters they are sent', () => {
    const link = {
      status: [ID_A],
      priority: ['high' as const],
      assignee: ID_B,
      group: ID_B,
      reporter: ID_A,
      channel: ['web' as const],
      sla: 'breached' as const,
      updated_from: '2026-10-01',
    }
    expect(activeFilterCount(link, false)).toBe(2)
    expect(activeFilterCount(link, true)).toBe(8)
    expect(activeFilterCount(link)).toBe(8)
  })

  it('counts a filter once, however many values it has', () => {
    expect(activeFilterCount({ status: [ID_A, ID_B], priority: ['high'], created_from: '2026-10-01', created_to: '2026-10-31' })).toBe(3)
  })

  it('awaiting_reply is one filter, counted where the view offers it', () => {
    expect(activeFilterCount({ awaiting_reply: true })).toBe(1)
    expect(activeFilterCount({ awaiting_reply: true }, true, VIEW_FILTERS.available)).toBe(0)
    expect(activeFilterCount({ awaiting_reply: true }, false)).toBe(0)
  })

  it('text, the queue, the toggles and the sort are not filters', () => {
    expect(activeFilterCount({ q: 'x', scope: 'all', closed: true, replies: false, sort: 'subject', page: 2 })).toBe(0)
  })

  it('hasNarrowing is true for text or any filter', () => {
    expect(hasNarrowing({})).toBe(false)
    expect(hasNarrowing({ q: '  ' })).toBe(false)
    expect(hasNarrowing({ q: 'x' })).toBe(true)
    expect(hasNarrowing({ item: 'i1' })).toBe(true)
  })
})

describe('clearing', () => {
  const full: TicketSearch = {
    q: 'x',
    scope: 'all',
    status: [ID_A],
    priority: ['high'],
    category: 'c1',
    type: 't1',
    item: 'i1',
    created_from: '2026-10-01',
    created_to: '2026-10-31',
    sort: 'subject',
    order: 'asc',
    page: 3,
  }

  it('one chip removes its own filter and returns to the first page', () => {
    const s = clearFilter(full, 'priority')
    expect(s.priority).toBeUndefined()
    expect(s.status).toEqual([ID_A])
    expect(s.page).toBeUndefined()
  })

  it('the awaiting chip clears awaiting_reply and nothing else', () => {
    const s = clearFilter({ ...full, awaiting_reply: true }, 'awaiting')
    expect(s.awaiting_reply).toBeUndefined()
    expect(s.priority).toEqual(['high'])
    expect(s.page).toBeUndefined()
  })

  it('the Created chip clears both ends', () => {
    const s = clearFilter(full, 'created')
    expect(s.created_from).toBeUndefined()
    expect(s.created_to).toBeUndefined()
  })

  it('the Category chip clears the type and item under it, which mean nothing without it', () => {
    const s = clearFilter(full, 'category')
    expect([s.category, s.type, s.item]).toEqual([undefined, undefined, undefined])
  })

  it('Clear all clears every filter, and leaves the search text, queue and sort alone', () => {
    const s = clearAll(full)
    expect(activeFilterCount(clearAll({ ...full, awaiting_reply: true }))).toBe(0)
    expect(clearAll({ ...full, awaiting_reply: true }).awaiting_reply).toBeUndefined()
    expect(s).toMatchObject({ q: 'x', scope: 'all', sort: 'subject', order: 'asc' })
    expect(s.page).toBeUndefined()
  })
})

describe('effectiveSort', () => {
  it('is newest first with nothing set, and relevance for a text search', () => {
    expect(effectiveSort({})).toEqual({ sort: 'created', order: 'desc' })
    expect(effectiveSort({ q: 'x' })).toEqual({ sort: 'relevance', order: 'desc' })
    expect(effectiveSort({ q: ' ' })).toEqual({ sort: 'created', order: 'desc' })
  })

  it('is the chosen column, in its natural order when none is given', () => {
    expect(effectiveSort({ q: 'x', sort: 'subject' })).toEqual({ sort: 'subject', order: 'asc' })
    expect(effectiveSort({ sort: 'updated', order: 'asc' })).toEqual({ sort: 'updated', order: 'asc' })
  })
})

describe('nextSort: clicking a header', () => {
  it('a new column starts in its natural direction', () => {
    expect(nextSort({}, 'subject')).toEqual({ sort: 'subject', order: 'asc' })
    expect(nextSort({}, 'updated')).toEqual({ sort: 'updated', order: 'desc' })
    expect(nextSort({ sort: 'subject', order: 'desc' }, 'priority')).toEqual({ sort: 'priority', order: 'asc' })
  })

  it('the same column again reverses', () => {
    expect(nextSort({ sort: 'subject', order: 'asc' }, 'subject')).toEqual({ sort: 'subject', order: 'desc' })
    expect(nextSort({ sort: 'subject', order: 'desc' }, 'subject')).toEqual({ sort: 'subject', order: 'asc' })
  })

  it('reverses from the natural direction when only the sort is in the address', () => {
    expect(nextSort({ sort: 'created' }, 'created')).toEqual({ sort: 'created', order: 'asc' })
  })

  // With no sort and no text the list is already newest first, so the first
  // click on Created has to reverse that, or it would appear to do nothing.
  it('the default order counts as a click already made on Created', () => {
    expect(nextSort({}, 'created')).toEqual({ sort: 'created', order: 'asc' })
  })

  it('but a text search is ranked, not by date, so Created starts newest first', () => {
    expect(nextSort({ q: 'x' }, 'created')).toEqual({ sort: 'created', order: 'desc' })
  })
})
