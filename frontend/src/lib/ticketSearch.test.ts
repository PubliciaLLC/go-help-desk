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
  const staff = { staff: true, admin: false, onlyQueue: false }
  const admin = { staff: true, admin: true, onlyQueue: false }
  const requester = { staff: false, admin: false, onlyQueue: false }

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

  it('a staff search with any filter covers everything they can see', () => {
    expect(toApiParams({ q: 'x' }, staff).scope).toBe('visible')
    expect(toApiParams({ priority: ['high'] }, staff).scope).toBe('visible')
    expect(toApiParams({ sla: 'breached' }, staff).scope).toBe('visible')
  })

  it('with no filter, the queue is the queue', () => {
    expect(toApiParams({}, staff).scope).toBe('mine')
    expect(toApiParams({ scope: 'unassigned' }, admin).scope).toBe('unassigned')
  })

  it('the Only <queue> chip narrows a search back to the queue', () => {
    expect(toApiParams({ q: 'x' }, { ...staff, onlyQueue: true }).scope).toBe('mine')
    expect(toApiParams({ q: 'x', scope: 'unassigned' }, { ...admin, onlyQueue: true }).scope).toBe('unassigned')
  })

  it('the closed toggle and the sort do not count as filters', () => {
    expect(toApiParams({ closed: true, sort: 'subject' }, staff).scope).toBe('mine')
  })

  it('names a person or an account, and the queue is then not sent (the server overrides it)', () => {
    expect(toApiParams({ reporter: ID_A }, staff)).toMatchObject({ reporter_id: ID_A })
    expect(toApiParams({ reporter: ID_A }, staff).scope).toBeUndefined()
    expect(toApiParams({ requester: 'whit' }, staff).scope).toBeUndefined()
    expect(toApiParams({ group: 'g1' }, staff).scope).toBeUndefined()
  })

  it('only an administrator picks another queue', () => {
    expect(toApiParams({ scope: 'all' }, staff).scope).toBe('mine')
    expect(toApiParams({ scope: 'all' }, admin).scope).toBe('all')
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
  })
})

describe('hasNarrowing / activeFilterCount', () => {
  it('counts a filter once, however many values it has', () => {
    expect(activeFilterCount({ status: [ID_A, ID_B], priority: ['high'], created_from: '2026-10-01', created_to: '2026-10-31' })).toBe(3)
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
    expect(activeFilterCount(s)).toBe(0)
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
