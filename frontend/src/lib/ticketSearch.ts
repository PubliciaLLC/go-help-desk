import type { Priority, SourceChannel } from '@/api/types'

// The ticket list's search state, in the address bar. The contract it maps to
// is ticket.Filter in the backend (internal/domain/ticket/filter.go); the names
// and values on the API side are copied from there.
//
// The page keeps the whole search in the URL so a copied link reproduces it.

export const PRIORITIES: Priority[] = ['critical', 'high', 'medium', 'low']
// The API name for the guest form is `guest`; the page says "Email / guest".
export const CHANNELS: SourceChannel[] = ['web', 'guest', 'phone', 'api', 'mcp']
export const SLA_STATES = ['breached', 'at_risk'] as const
export type SLAFilter = (typeof SLA_STATES)[number]

export const SORT_KEYS = ['number', 'subject', 'status', 'priority', 'sla', 'created', 'updated'] as const
export type SortKey = (typeof SORT_KEYS)[number]
export type SortOrder = 'asc' | 'desc'

// The direction a column starts in when first clicked. Dates run newest first;
// for priority and SLA ascending means most urgent first.
const NATURAL_ORDER: Record<SortKey, SortOrder> = {
  number: 'asc',
  subject: 'asc',
  status: 'asc',
  priority: 'asc',
  sla: 'asc',
  created: 'desc',
  updated: 'desc',
}

// Sorting by what a requester is never sent would show them its order (the
// same leak as filtering on it), so the server answers 403.
const REQUESTER_SORTS: SortKey[] = ['number', 'subject', 'status', 'priority', 'created']

// The address's old queue, read only to translate a link made before views
// (#676). The page never writes it and the API scope is derived from the view.
export type Queue = 'mine' | 'unassigned' | 'all'

// The ticket list's views (#676): Assigned to Me, My Group's Tickets,
// Available and Search. A requester has none.
export const VIEWS = ['me', 'groups', 'available', 'search'] as const
export type View = (typeof VIEWS)[number]

// Who personally holds a ticket, relative to the viewer: the three filters of
// My Group's Tickets. Absent is "All".
export const HELD_BY = ['nobody', 'me', 'others'] as const
export type HeldBy = (typeof HELD_BY)[number]

export const VIEW_NAME: Record<View, string> = {
  me: 'Assigned to Me',
  groups: "My Group's Tickets",
  available: 'Available',
  search: 'Search',
}

export const HELD_BY_LABEL: Record<HeldBy | 'all', string> = {
  all: 'All',
  nobody: 'Group queue',
  me: 'Assigned to me',
  others: 'Assigned to others',
}

// Dates are YYYY-MM-DD in the address and become instants in the viewer's own
// timezone (owner decision S8) when sent.
export interface TicketSearch {
  q?: string
  // Stored only when off: searching replies is the page's default.
  replies?: false
  scope?: Queue
  view?: View
  // Kept only with view=groups.
  held_by?: HeldBy
  status?: string[]
  priority?: Priority[]
  category?: string
  type?: string
  item?: string
  assignee?: string
  group?: string
  // reporter is an account (a pick from the directory); requester is free text.
  reporter?: string
  requester?: string
  created_from?: string
  created_to?: string
  updated_from?: string
  updated_to?: string
  channel?: SourceChannel[]
  sla?: SLAFilter
  // Stored only when on: the ticket's newest public reply is the requester's.
  // Named as the address spells it, like the other filters. Offered on Assigned
  // to Me and My Group's Tickets, and never sent to a requester (DESIGN.md,
  // Dashboard).
  awaiting_reply?: true
  // Stored only when on: closed tickets are left out by default.
  closed?: true
  sort?: SortKey
  order?: SortOrder
  // 1-based, stored from 2.
  page?: number
}

// The chips, and what each clears. A chip is one filter to the person even when
// it is two parameters (a date range, a category and what is under it).
export type FilterKey =
  | 'status' | 'priority' | 'category' | 'type' | 'item' | 'assignee' | 'group'
  | 'requester' | 'created' | 'updated' | 'channel' | 'sla' | 'awaiting'

const FILTER_FIELDS: Record<FilterKey, (keyof TicketSearch)[]> = {
  status: ['status'],
  priority: ['priority'],
  // A type or item means nothing without the category above it.
  category: ['category', 'type', 'item'],
  type: ['type', 'item'],
  item: ['item'],
  assignee: ['assignee'],
  group: ['group'],
  requester: ['reporter', 'requester'],
  created: ['created_from', 'created_to'],
  updated: ['updated_from', 'updated_to'],
  channel: ['channel'],
  sla: ['sla'],
  awaiting: ['awaiting_reply'],
}

const FILTER_KEYS = Object.keys(FILTER_FIELDS) as FilterKey[]

// The filters each view offers. Assigned to Me leaves out Assignee (it is always
// me); Available leaves out Assignee and Group (always empty for that queue) and
// Requester (the server lets a named set replace the unassigned queue, which
// would list tickets that are not available). Awaiting my reply is offered on
// Assigned to Me and My Group's Tickets only: a reply lookup per ticket is cheap
// on a person's or a group's queue and not on the whole desk (DESIGN.md,
// Dashboard). A filter a view does not offer is neither shown nor sent, even if
// the address carries it.
export const VIEW_FILTERS: Record<View, readonly FilterKey[]> = {
  me: FILTER_KEYS.filter(k => k !== 'assignee'),
  groups: FILTER_KEYS,
  available: FILTER_KEYS.filter(k => k !== 'assignee' && k !== 'group' && k !== 'requester' && k !== 'awaiting'),
  search: FILTER_KEYS.filter(k => k !== 'awaiting'),
}

// --- URL to state ------------------------------------------------------------

// TanStack Router's default parser JSON-parses every value: "2026" arrives as a
// number and "true" as a boolean. A search for either must still be text.
function text(v: unknown): string | undefined {
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  if (typeof v !== 'string') return undefined
  const t = v.trim()
  return t === '' ? undefined : t
}

// One value or several, kept once each. The old status=<id> link is one value.
function list(v: unknown): string[] | undefined {
  const items = Array.isArray(v) ? v : [v]
  const out = [...new Set(items.map(text).filter((s): s is string => s !== undefined))]
  return out.length > 0 ? out : undefined
}

// An id the filters send: category, type, item, status, assignee, group and
// reporter are all UUIDs. The address is typed by anyone and some of these end
// up in request paths, so a value that is not shaped like one never gets that far.
const ID_SHAPE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

function id(v: unknown): string | undefined {
  const t = text(v)
  return t !== undefined && ID_SHAPE.test(t) ? t : undefined
}

// The server refuses more than this many statuses; the address keeps the first.
const MAX_STATUSES = 20

function ids(v: unknown): string[] | undefined {
  const out = list(v)?.filter(s => ID_SHAPE.test(s)).slice(0, MAX_STATUSES)
  return out && out.length > 0 ? out : undefined
}

// Free-text requester: the server holds it to 2-254 characters and answers 400
// to anything else, so the address drops what is outside that.
function requesterText(v: unknown): string | undefined {
  const t = text(v)
  if (t === undefined) return undefined
  const n = [...t].length
  return n >= 2 && n <= 254 ? t : undefined
}

function oneOf<T extends string>(v: unknown, allowed: readonly T[]): T | undefined {
  return typeof v === 'string' && (allowed as readonly string[]).includes(v) ? (v as T) : undefined
}

function listOf<T extends string>(v: unknown, allowed: readonly T[]): T[] | undefined {
  const out = list(v)?.filter((s): s is T => (allowed as readonly string[]).includes(s))
  return out && out.length > 0 ? out : undefined
}

function day(v: unknown): string | undefined {
  const s = text(v)
  if (!s || !/^\d{4}-\d{2}-\d{2}$/.test(s)) return undefined
  // 2026-02-30 matches the pattern and is not a day.
  const [y, m, d] = s.split('-').map(Number)
  const date = new Date(y, m - 1, d)
  return date.getFullYear() === y && date.getMonth() === m - 1 && date.getDate() === d ? s : undefined
}

function isOff(v: unknown) {
  return v === false || v === 'false' || v === '0' || v === 'off'
}

function isOn(v: unknown) {
  return v === true || v === 'true' || v === '1'
}

// Never throws and never trusts: the address is typed by anyone. What it
// cannot read it drops, which is what the server would refuse with 400.
export function parseTicketSearch(raw: Record<string, unknown>): TicketSearch {
  const sort = oneOf(raw.sort, SORT_KEYS)
  const page = Number(raw.page)
  const reporter = id(raw.reporter)
  const view = oneOf(raw.view, VIEWS)
  const s: TicketSearch = {
    q: text(raw.q),
    replies: isOff(raw.replies) ? false : undefined,
    scope: oneOf(raw.scope, ['mine', 'unassigned', 'all'] as const),
    view,
    held_by: view === 'groups' ? oneOf(raw.held_by, HELD_BY) : undefined,
    status: ids(raw.status),
    priority: listOf(raw.priority, PRIORITIES),
    category: id(raw.category),
    type: id(raw.type),
    item: id(raw.item),
    assignee: id(raw.assignee),
    group: id(raw.group),
    reporter,
    // An account is what the chip names and the page sends; with both in an
    // address (the form cannot make that) the account wins.
    requester: reporter ? undefined : requesterText(raw.requester),
    created_from: day(raw.created_from),
    created_to: day(raw.created_to),
    updated_from: day(raw.updated_from),
    updated_to: day(raw.updated_to),
    channel: listOf(raw.channel, CHANNELS),
    sla: oneOf(raw.sla, SLA_STATES),
    awaiting_reply: isOn(raw.awaiting_reply) ? true : undefined,
    closed: isOn(raw.closed) ? true : undefined,
    sort,
    // An order with no sort is ignored by the server; do not keep it.
    order: sort ? oneOf(raw.order, ['asc', 'desc'] as const) : undefined,
    page: Number.isInteger(page) && page >= 2 ? page : undefined,
  }
  return toUrlSearch(s)
}

// Drops everything empty, so the address holds only what is set.
export function toUrlSearch(s: TicketSearch): TicketSearch {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(s)) {
    if (v === undefined || v === '' || (Array.isArray(v) && v.length === 0)) continue
    out[k] = v
  }
  return out as TicketSearch
}

// --- Filters -----------------------------------------------------------------

const isSet = (v: unknown) => v !== undefined && v !== '' && !(Array.isArray(v) && v.length === 0)

export function isFilterActive(s: TicketSearch, key: FilterKey): boolean {
  return FILTER_FIELDS[key].some(f => isSet(s[f]))
}

// How many chips there would be. Text, the queue, the toggles and the sort
// are not filters.
//
// A requester is sent only some of the filters (see toApiParams), so a staff
// link opened by one counts just those: the badge should match what is applied.
const REQUESTER_FILTER_KEYS: FilterKey[] = ['status', 'priority', 'category', 'created']

// `offered` is what the view offers (VIEW_FILTERS): a filter it leaves out is
// not applied, so it is not counted.
export function activeFilterCount(s: TicketSearch, staff = true, offered: readonly FilterKey[] = FILTER_KEYS): number {
  // A type or item alone (no category) is not a thing the form can make, and a
  // typed address can: count it once, under the category.
  return FILTER_KEYS.filter(
    k =>
      k !== 'type' &&
      k !== 'item' &&
      offered.includes(k) &&
      (staff || REQUESTER_FILTER_KEYS.includes(k)) &&
      isFilterActive(s, k),
  ).length
}

// Whether the viewer is narrowing the list: text, or any filter. A staff search
// that narrows covers everything they can see rather than just their queue.
export function hasNarrowing(s: TicketSearch): boolean {
  return (s.q ?? '').trim() !== '' || FILTER_KEYS.some(k => isFilterActive(s, k))
}

export function clearFilter(s: TicketSearch, key: FilterKey): TicketSearch {
  const next = { ...s }
  for (const f of FILTER_FIELDS[key]) delete next[f]
  delete next.page
  return next
}

// Every parameter that makes up a filter, flat: what the phone dialog copies
// in as a draft and what Show results writes back.
export const FILTER_FIELD_NAMES = [...new Set(FILTER_KEYS.flatMap(k => FILTER_FIELDS[k]))]

export function clearAll(s: TicketSearch): TicketSearch {
  let next = s
  for (const k of FILTER_KEYS) next = clearFilter(next, k)
  return next
}

// --- Sort --------------------------------------------------------------------

// What order the list is in now, which is not always what is in the address:
// no sort means newest first, or best match first when there is text.
export function effectiveSort(s: Pick<TicketSearch, 'q' | 'sort' | 'order'>): { sort: SortKey | 'relevance'; order: SortOrder } {
  if (!s.sort) return { sort: (s.q ?? '').trim() !== '' ? 'relevance' : 'created', order: 'desc' }
  return { sort: s.sort, order: s.order ?? NATURAL_ORDER[s.sort] }
}

// A click on a header: a new column starts in its natural direction, the same
// column again reverses. The page always sends an explicit order.
export function nextSort(s: Pick<TicketSearch, 'q' | 'sort' | 'order'>, key: SortKey): { sort: SortKey; order: SortOrder } {
  const now = effectiveSort(s)
  if (now.sort === key) return { sort: key, order: now.order === 'asc' ? 'desc' : 'asc' }
  return { sort: key, order: NATURAL_ORDER[key] }
}

// Whether a header may be clicked: the server answers 403 to a requester who
// sorts by something they are never sent.
export function canSort(key: SortKey, staff: boolean): boolean {
  return staff || REQUESTER_SORTS.includes(key)
}

export const SORT_LABEL: Record<SortKey, string> = {
  number: 'Ticket',
  subject: 'Subject',
  status: 'Status',
  priority: 'Priority',
  sla: 'SLA',
  created: 'Created',
  updated: 'Updated',
}

// --- Dates -------------------------------------------------------------------

// Local midnight, built from the parts so that a daylight-saving change does
// not shift it (adding 24 hours of milliseconds would).
function localMidnight(ymd: string, addDays: number): Date {
  const [y, m, d] = ymd.split('-').map(Number)
  return new Date(y, m - 1, d + addDays)
}

// The first instant of a day in the viewer's timezone: created_after, inclusive.
export const dayStart = (ymd: string) => localMidnight(ymd, 0).toISOString()
// The first instant of the NEXT day: created_before is exclusive, and the end
// date the person picked is included.
export const dayEnd = (ymd: string) => localMidnight(ymd, 1).toISOString()

// --- State to API ------------------------------------------------------------

// A page of results. The server returns an array with no total, so a full page
// is the only sign there may be another.
export const PAGE_SIZE = 50

export interface Viewer {
  // Staff or administrator.
  staff: boolean
  admin: boolean
  // GET /me said this person may open the Available view (an administrator
  // always may).
  canUseAvailable: boolean
  // GET /me said SLA tracking is on. The SLA filter and sort are refused with
  // 400 while it is off, so they are not sent unless it is known to be on.
  slaOn: boolean
}

export type ApiQueue = 'mine' | 'assigned_to_me' | 'my_groups' | 'unassigned' | 'all' | 'visible'

export interface TicketListParams {
  q?: string
  search_replies?: boolean
  scope?: ApiQueue
  held_by?: HeldBy
  status_id?: string[]
  include_closed?: boolean
  priority?: Priority[]
  category_id?: string
  type_id?: string
  item_id?: string
  assignee_user_id?: string
  assignee_group_id?: string
  reporter_id?: string
  requester?: string
  created_after?: string
  created_before?: string
  updated_after?: string
  updated_before?: string
  source_channel?: SourceChannel[]
  sla?: SLAFilter
  awaiting_reply?: true
  sort?: SortKey
  order?: SortOrder
  limit: number
  offset: number
}

// --- Views -------------------------------------------------------------------

export interface ViewViewer {
  staff: boolean
  admin: boolean
  canUseAvailable: boolean
}

// The view an address should carry, or undefined for a requester, who has none.
// An address with a view keeps it (a view the viewer may not use is Assigned to
// Me, with no error: a colleague's shared link must not greet them with a 403).
// One without is a link made before #676 and is translated (DESIGN.md, Ticket
// list views, Addresses): text or any filter is Search, which is what a staff
// search that narrowed covered before; scope=all is Search; scope=unassigned is
// Available; anything else is Assigned to Me.
export function resolveView(raw: object, viewer: ViewViewer): View | undefined {
  if (!viewer.staff) return undefined
  const s = parseTicketSearch(raw as Record<string, unknown>)
  let view: View = s.view ?? (hasNarrowing(s) || s.scope === 'all' ? 'search' : s.scope === 'unassigned' ? 'available' : 'me')
  if (view === 'available' && !(viewer.admin || viewer.canUseAvailable)) view = 'me'
  return view
}

// The address that replaces the one the page was opened with: the resolved
// view written, scope dropped, held_by kept only on My Group's Tickets. A
// requester's view, scope and held_by are dropped.
export function translateAddress(raw: object, viewer: ViewViewer): TicketSearch {
  const s = parseTicketSearch(raw as Record<string, unknown>)
  const view = resolveView(s, viewer)
  return toUrlSearch({ ...s, scope: undefined, view, held_by: view === 'groups' ? s.held_by : undefined })
}

const SCOPE_OF_VIEW: Record<View, ApiQueue> = {
  me: 'assigned_to_me',
  groups: 'my_groups',
  available: 'unassigned',
  search: 'visible',
}

// The request for a search. Anything a requester may not use is dropped here
// rather than sent: the server answers 403 to the whole request, and a link
// copied from a colleague's staff search would otherwise show a requester an
// error instead of their own tickets.
//
// A staff member's request always carries the scope of the view. A search that
// names a reporter, a requester or a group would otherwise send none, and the
// server reads an absent scope as mine, which would widen the list.
export function toApiParams(s: TicketSearch, viewer: Viewer): TicketListParams {
  const p: TicketListParams = { limit: PAGE_SIZE, offset: ((s.page ?? 1) - 1) * PAGE_SIZE }
  const set = <K extends keyof TicketListParams>(k: K, v: TicketListParams[K] | undefined) => {
    if (isSet(v)) p[k] = v as TicketListParams[K]
  }
  const view = resolveView(s, viewer)
  const offers = (k: FilterKey) => view === undefined || VIEW_FILTERS[view].includes(k)

  const q = (s.q ?? '').trim()
  set('q', q || undefined)
  // The API default is off; the page's is on.
  if (q && s.replies !== false) p.search_replies = true

  set('status_id', s.status)
  // The server ignores this when a status is named, so sending it is harmless
  // and the rule stays in one place.
  if (!s.closed) p.include_closed = false
  set('priority', s.priority)
  set('category_id', s.category)
  set('type_id', s.type)
  set('item_id', s.item)
  set('created_after', s.created_from ? dayStart(s.created_from) : undefined)
  set('created_before', s.created_to ? dayEnd(s.created_to) : undefined)

  if (viewer.staff) {
    if (offers('assignee')) set('assignee_user_id', s.assignee)
    if (offers('group')) set('assignee_group_id', s.group)
    if (offers('requester')) {
      set('reporter_id', s.reporter)
      set('requester', s.requester)
    }
    set('updated_after', s.updated_from ? dayStart(s.updated_from) : undefined)
    set('updated_before', s.updated_to ? dayEnd(s.updated_to) : undefined)
    set('source_channel', s.channel)
    if (viewer.slaOn) set('sla', s.sla)
    if (offers('awaiting')) set('awaiting_reply', s.awaiting_reply)
  }

  if (s.sort && (viewer.staff || REQUESTER_SORTS.includes(s.sort)) && (s.sort !== 'sla' || viewer.slaOn)) {
    p.sort = s.sort
    p.order = s.order ?? NATURAL_ORDER[s.sort]
  }

  // A requester has no views: their request is what it was before #676.
  if (view === undefined) {
    p.scope = 'mine'
  } else {
    p.scope = SCOPE_OF_VIEW[view]
    if (view === 'groups') set('held_by', s.held_by)
  }
  return p
}
