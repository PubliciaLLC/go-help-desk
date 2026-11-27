import { useState, useEffect, useMemo, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { getTicket, searchTickets, updateTicket } from '@/api/tickets'
import { listStatuses } from '@/api/admin'
import { getMe } from '@/api/auth'
import { useAuthStore } from '@/store/auth'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { ArrowDownIcon, ArrowUpIcon, ChevronsUpDownIcon, PlusIcon, SearchIcon, SlidersHorizontalIcon } from 'lucide-react'
import { priorityVariant } from '@/lib/format'
import { ReopenedBadge } from '@/components/ticket/ReopenedBadge'
import { QuarantinedBadge } from '@/components/ticket/QuarantinedBadge'
import { SLAIndicator } from '@/components/ticket/SLAIndicator'
import { FilterChips, type ChipNames } from '@/components/ticket/FilterChips'
import { FilterDialog } from '@/components/ticket/FilterDialog'
import { ReplyMatchLine } from '@/components/ticket/ReplyMatch'
import { TicketFilters } from '@/components/ticket/TicketFilters'
import { apiRefusal, extractError } from '@/api/client'
import {
  PAGE_SIZE,
  SORT_KEYS,
  SORT_LABEL,
  activeFilterCount,
  canSort,
  clearAll,
  clearFilter,
  effectiveSort,
  FILTER_FIELD_NAMES,
  HELD_BY,
  HELD_BY_LABEL,
  nextSort,
  parseTicketSearch,
  resolveView,
  toApiParams,
  toUrlSearch,
  translateAddress,
  VIEW_FILTERS,
  VIEW_NAME,
  type FilterKey,
  type HeldBy,
  type SortKey,
  type TicketSearch,
  type View,
} from '@/lib/ticketSearch'
import { useFilterLookups } from '@/lib/useFilterLookups'
import { useMediaQuery } from '@/lib/useMediaQuery'

// What an empty list says when the box and the filters are empty (DESIGN.md,
// Ticket list views). A requester has no views and keeps the words they had.
const EMPTY_BY_HELD: Record<HeldBy | 'all', string> = {
  all: 'No tickets are assigned to your groups.',
  nobody: 'No group tickets are waiting for someone to take them.',
  me: "None of your groups' tickets are assigned to you.",
  others: "None of your groups' tickets are held by a teammate.",
}

function emptyMessageFor(view: View | undefined, heldBy: HeldBy | undefined) {
  switch (view) {
    case 'me':
      return 'No tickets are assigned to you.'
    case 'groups':
      return EMPTY_BY_HELD[heldBy ?? 'all']
    case 'available':
      return 'No tickets are waiting without a group or a person.'
    case 'search':
      return 'No tickets yet.'
    default:
      return 'No tickets are currently assigned to you or your groups.'
  }
}

// "3 Nov", with the year once it is not this one: the table is tight and the
// full date is in the cell's title. The viewer's own locale and timezone, like
// every other date on the page.
function shortDate(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const sameYear = d.getFullYear() === new Date().getFullYear()
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', ...(sameYear ? {} : { year: 'numeric' }) })
}

// What the order means for a column, for the line under the toolbar.
function orderWords(sort: SortKey | 'relevance', order: 'asc' | 'desc') {
  if (sort === 'relevance') return 'best match first'
  if (sort === 'created' || sort === 'updated') return order === 'desc' ? 'newest first' : 'oldest first'
  if (sort === 'priority' || sort === 'sla') return order === 'asc' ? 'most urgent first' : 'least urgent first'
  return order === 'asc' ? 'ascending' : 'descending'
}

// A column heading that sorts. The button, not the cell, takes the click, so
// the heading is reachable by keyboard; aria-sort says which way the table is
// ordered now. Without it the visible arrow is the only sign.
function SortHeader({
  k,
  now,
  staff,
  onSort,
  className = '',
  tight = false,
}: {
  k: SortKey
  now: { sort: SortKey | 'relevance'; order: 'asc' | 'desc' }
  staff: boolean
  onSort: (k: SortKey) => void
  className?: string
  // Less side padding, for the two date columns: on Linux fonts the table was
  // 1px wider than its box with px-3 everywhere, and the box clips.
  tight?: boolean
}) {
  const active = now.sort === k
  // px-3, not px-4: with the SLA, Created and Updated columns the table is
  // 976px wide at most, and its box clips rather than scrolls.
  const th = `${tight ? 'px-2' : 'px-3'} py-2 text-left ${className}`
  if (!canSort(k, staff)) return <th className={th}>{SORT_LABEL[k]}</th>
  return (
    <th className={th} aria-sort={active ? (now.order === 'asc' ? 'ascending' : 'descending') : undefined}>
      <button
        type="button"
        onClick={() => onSort(k)}
        className="-mx-1 inline-flex items-center gap-1 rounded-sm px-1 uppercase tracking-wider hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
      >
        {SORT_LABEL[k]}
        {active ? (
          now.order === 'asc' ? <ArrowUpIcon aria-hidden="true" className="h-3 w-3" /> : <ArrowDownIcon aria-hidden="true" className="h-3 w-3" />
        ) : (
          <ChevronsUpDownIcon aria-hidden="true" className="h-3 w-3 text-gray-400" />
        )}
      </button>
    </th>
  )
}

export function TicketListPage() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  // The whole search lives in the address (lib/ticketSearch.ts). The router's
  // validateSearch has already cleaned it; parsing again here costs nothing and
  // keeps this page honest if it is ever given a raw one.
  const search = parseTicketSearch(useSearch({ from: '/tickets' }) as Record<string, unknown>)
  const { user } = useAuthStore()
  const isStaffOrAdmin = user?.role === 'staff' || user?.role === 'admin'
  const isAdmin = user?.role === 'admin'

  // Change the address. `replace` for what is typed or toggled in place, so the
  // Back button is not a record of every keystroke.
  function update(fn: (s: TicketSearch) => TicketSearch, replace = false) {
    navigate({
      to: '/tickets',
      search: (prev: TicketSearch) => toUrlSearch(fn(parseTicketSearch(prev as Record<string, unknown>))),
      replace,
    })
  }

  // The box and the queue are held here as well as in the address: typing must
  // not wait for the router, and the debounce below is what writes the address.
  // An address that changes from outside (Back, a pasted link) is adopted.
  const [query, setQuery] = useState(search.q ?? '')
  const [debouncedQuery, setDebouncedQuery] = useState(search.q ?? '')
  const [seenQ, setSeenQ] = useState(search.q)
  if (search.q !== seenQ) {
    setSeenQ(search.q)
    if ((search.q ?? '') !== debouncedQuery.trim()) {
      setQuery(search.q ?? '')
      setDebouncedQuery(search.q ?? '')
    }
  }
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [bulkStatusId, setBulkStatusId] = useState('')
  const [bulkError, setBulkError] = useState('')
  const [jumpError, setJumpError] = useState('')
  const [filtersOpen, setFiltersOpen] = useState(false)
  const filtersButton = useRef<HTMLButtonElement>(null)
  // md is where the sidebar and the table appear; below it the filters are a
  // full-screen dialog and the results are cards.
  const wide = useMediaQuery('(min-width: 768px)', true)

  // 300 ms debounce on the search box, then into the address.
  useEffect(() => {
    const id = setTimeout(() => {
      setDebouncedQuery(query)
      const t = query.trim()
      if (t !== (search.q ?? '')) update(s => ({ ...s, q: t || undefined, page: undefined }), true)
    }, 300)
    return () => clearTimeout(id)
    // `update` and `navigate` are new every render; the timer must not restart for that.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, search.q])

  const { data: statuses = [] } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

  // Whether SLA tracking is on, and whether the Available view may be opened,
  // are what GET /me says, for staff and administrators. The sign-in response's
  // user does not carry them and the store is not refreshed after that, so the
  // page asks /me itself; Settings makes this query stale when it saves. Absent
  // is unknown, and unknown hides it rather than guessing from the rows: the SLA
  // filter and sort are refused with 400 while tracking is off. Until /me has
  // answered the store's copy stands in, and the list waits for the answer so
  // its first request is right.
  //
  // A requester asks it too, for one thing: whether Items is shown to them
  // (#711). Until it says so, or when it says no, the address's `item` is not
  // sent and not shown, so a bookmark from before, or a switch turned off while
  // the page is open, does not meet a 403.
  const { data: me, isFetched: meSettled } = useQuery({
    queryKey: ['me'],
    queryFn: getMe,
    retry: false,
  })
  const hideItem = !isStaffOrAdmin && me?.can_choose_item !== true
  const slaOn = (me?.sla_enabled ?? user?.sla_enabled) === true

  // The view this address means (DESIGN.md, Ticket list views). Until /me has
  // answered an address that names Available is believed, so the heading does
  // not flash; nothing is requested or written before then.
  const canUseAvailable = isAdmin || (meSettled ? me?.can_use_available_queue === true : search.view === 'available')
  const viewer = { staff: isStaffOrAdmin, admin: isAdmin, canUseAvailable, slaOn }
  const view = resolveView(search, viewer)
  const heldBy = view === 'groups' ? search.held_by : undefined
  // The filters this view offers: the rest are neither shown nor sent.
  const offered = view ? VIEW_FILTERS[view] : undefined

  // An address made before #676 (no view, or a scope), or one this viewer may
  // not use as it stands, is replaced once /me has answered. Replaced, not
  // added, so Back does not return to the address that was translated.
  const rewrite =
    (!isStaffOrAdmin || meSettled) &&
    (search.scope !== undefined || search.view !== view || search.held_by !== heldBy)
  useEffect(() => {
    if (rewrite) update(s => translateAddress(s, viewer), true)
    // `update` and `viewer` are new every render; the address is what decides.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rewrite])

  // What is being asked for now. A search that differs from the address (typed,
  // not yet written) starts at the first page: otherwise a narrower search
  // lands on an offset past its own results and shows an empty list, which was
  // the flash the page number used to be held together with the search to avoid.
  const typed = debouncedQuery.trim()
  const effective: TicketSearch = {
    ...search,
    item: hideItem ? undefined : search.item,
    q: typed || undefined,
    view,
    held_by: heldBy,
    scope: undefined,
    page: typed !== (search.q ?? '') ? undefined : search.page,
  }

  // A saved link that names SLA, opened while tracking is off or unknown.
  const slaLinkDropped = isStaffOrAdmin && meSettled && !slaOn && Boolean(search.sla || search.sort === 'sla')

  const params = toApiParams(effective, viewer)
  const page = (effective.page ?? 1) - 1

  const { data: result, isFetching, error: listError } = useQuery({
    queryKey: ['tickets', params],
    queryFn: () => searchTickets(params),
    enabled: !isStaffOrAdmin || meSettled,
    // Keeping the previous page visible while the next loads stops the table
    // collapsing to empty on every click.
    placeholderData: (previous) => previous,
    // Re-polls only once this page has actually shown a live SLA status — an
    // instance with SLA tracking off, or a page of tickets with no matching
    // policy, never polls for it. See TicketDetailPage for the same choice.
    refetchInterval: (query) => (query.state.data?.tickets.some(t => t.sla != null) ? 60_000 : false),
  })
  const tickets = useMemo(() => result?.tickets ?? [], [result])

  // A full page means there is probably another. The server returns an array,
  // not a total, so this is what there is to go on — and it is enough for
  // next/previous.
  const mayHaveMore = tickets.length === PAGE_SIZE

  // The column waits for a row with a status to show, so an instance whose
  // tickets carry no policy sees the table it always saw.
  const showSLAColumn = useMemo(() => slaOn && tickets.some(t => t.sla != null), [slaOn, tickets])

  // Names for the chips. The panel loads the same lists for its selects.
  const lookups = useFilterLookups(search, { staff: isStaffOrAdmin, wantPeople: false })
  const chipNames: ChipNames = {
    status: id => statuses.find(s => s.id === id)?.name,
    category: id => lookups.categories.find(c => c.id === id)?.name,
    type: id => lookups.types.find(t => t.id === id)?.name,
    item: id => lookups.items.find(i => i.id === id)?.name,
    user: id => lookups.staff.find(u => u.id === id)?.display_name,
    group: id => lookups.groups.find(g => g.id === id)?.name,
  }

  // Only the filters this view offers count, show as chips and are sent.
  const filterCount = activeFilterCount(search, isStaffOrAdmin, offered)

  // The Search view is the one that covers everything the viewer can see; a
  // queue view stays in its queue however much is typed (#676).
  const searching = view === 'search'

  function patchFilters(p: Partial<TicketSearch>) {
    update(s => ({ ...s, ...p, page: undefined }), true)
  }

  // Show results in the phone dialog: what was in it replaces the filters.
  function applyDraft(draft: TicketSearch) {
    update(s => {
      const next: TicketSearch = { ...clearAll(s), page: undefined }
      for (const f of FILTER_FIELD_NAMES) {
        if (draft[f] !== undefined) (next as Record<string, unknown>)[f] = draft[f]
      }
      return next
    })
  }

  // Page numbers are 1-based in the address and stored from 2.
  function goPage(n: number) {
    update(s => ({ ...s, page: n >= 2 ? n : undefined }))
  }

  // A link's sort=sla is not sent while SLA is off, so the list is not in that order.
  const sortNow = effectiveSort(effective.sort === 'sla' && !slaOn ? { ...effective, sort: undefined, order: undefined } : effective)
  function sortBy(key: SortKey) {
    const { sort, order } = nextSort(effective, key)
    update(s => ({ ...s, sort, order, page: undefined }))
  }
  const sortableKeys = SORT_KEYS.filter(k => canSort(k, isStaffOrAdmin) && (k !== 'sla' || showSLAColumn))

  // Said under the table when the server could not search every reply: it sends
  // X-Replies-Searched: false when the reply arm gave up (the time cap), and
  // X-Reply-Index: building while the index is still being built.
  const askedForReplies = Boolean(params.search_replies)
  const replyNotice =
    result && askedForReplies
      ? result.replyIndex === 'building'
        ? 'Reply search is still being prepared, so these results cover ticket subjects and descriptions only. Try again later to include replies.'
        : result.repliesSearched === false
          ? "Replies weren't fully searched, so tickets that matched only through a reply may be missing. Try a narrower search, or search again."
          : ''
      : ''

  function statusFor(id: string) {
    return statuses.find(s => s.id === id)
  }

  const allSelected = tickets.length > 0 && tickets.every(t => selectedIds.has(t.id))
  const someSelected = selectedIds.size > 0

  function toggleAll() {
    if (allSelected) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(tickets.map(t => t.id)))
    }
  }

  function toggleOne(id: string) {
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  const bulkMutation = useMutation({
    // allSettled, not all: the calls go out in parallel, so when one is
    // refused the others have already gone through. Promise.all rejected on
    // the first failure and the page said nothing at all — leaving the
    // selection highlighted over tickets that HAD changed, with no way to
    // tell which. What comes back is a count and the server's own reason.
    mutationFn: async (statusId: string) => {
      const ids = [...selectedIds]
      const results = await Promise.allSettled(
        ids.map(id => updateTicket(id, { status_id: statusId })),
      )
      const refused = results.flatMap((r, i) =>
        r.status === 'rejected' ? [{ id: ids[i], reason: extractError(r.reason) }] : [],
      )
      return { total: ids.length, refused }
    },
    onSuccess: ({ total, refused }) => {
      qc.invalidateQueries({ queryKey: ['tickets'] })
      if (refused.length === 0) {
        setBulkError('')
        setSelectedIds(new Set())
        setBulkStatusId('')
        return
      }
      // The ones that failed stay selected, so the next attempt is just
      // another click on the same button.
      setSelectedIds(new Set(refused.map(r => r.id)))
      setBulkError(
        `${total - refused.length} of ${total} updated. ${refused.length} refused: ${refused[0].reason}`,
      )
    },
    onError: (err) => setBulkError(extractError(err)),
  })

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="text-2xl font-bold text-gray-900">{view ? VIEW_NAME[view] : 'Tickets'}</h1>
          <Link to="/tickets/new">
            <Button>
              <PlusIcon className="mr-2 h-4 w-4" />
              New Ticket
            </Button>
          </Link>
        </div>

        {/* Toolbar: search + scope + closed toggle */}
        <div className="flex items-center gap-3 flex-wrap">
          <div className="relative w-full md:flex-1 md:max-w-lg">
            <SearchIcon className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" />
            <Input
              className="pl-9"
              aria-label="Search tickets"
              placeholder="Search by tracking number, subject, or description…"
              value={query}
              onChange={e => setQuery(e.target.value)}
            />
          </div>
          <Button
            ref={filtersButton}
            type="button"
            variant="outline"
            aria-expanded={filtersOpen}
            onClick={() => setFiltersOpen(o => !o)}
            className="gap-2 pointer-coarse:min-h-11"
          >
            <SlidersHorizontalIcon aria-hidden="true" className="h-4 w-4" />
            Filters
            {filterCount > 0 && (
              <span
                aria-label={`${filterCount} active`}
                className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-blue-600 px-1 text-xs font-medium text-white"
              >
                {filterCount}
              </span>
            )}
          </Button>
          {isStaffOrAdmin && (
            <Button
              type="button"
              variant="outline"
              onClick={async () => {
                const q = query.trim()
                if (!q) return
                setJumpError('')
                try {
                  const t = await getTicket(q)
                  navigate({ to: '/tickets/$id', params: { id: t.id } })
                } catch (err) {
                  // DESIGN.md's "Ticket Search" says this shows an inline
                  // error. It used to swallow every failure — a mistyped
                  // number, a ticket outside this staff member's scope and a
                  // server fault all looked identical, which is to say they
                  // looked like the button doing nothing.
                  const { status, message } = apiRefusal(err)
                  setJumpError(
                    status === 404 ? `No ticket matches "${q}".`
                    : message ? message
                    : `That ticket could not be opened${status ? ` (error ${status})` : ''}.`,
                  )
                }
              }}
            >
              Jump to ticket
            </Button>
          )}
          {/* Their own row: with the filters button they would take room the
              search box is there to use (#414 pins its width). */}
          <div className="flex w-full flex-wrap items-center gap-x-5 gap-y-1">
            <label className="flex min-h-7 items-center gap-2 text-sm text-gray-600 cursor-pointer select-none whitespace-nowrap pointer-coarse:min-h-11">
              <input
                type="checkbox"
                className="h-4 w-4 rounded border-gray-300"
                checked={Boolean(search.closed)}
                onChange={e => patchFilters({ closed: e.target.checked ? true : undefined })}
              />
              Include closed
            </label>
            {/* On unless turned off, and only meaningful with text in the box. */}
            <label className="flex min-h-7 items-center gap-2 text-sm text-gray-600 cursor-pointer select-none whitespace-nowrap pointer-coarse:min-h-11">
              <input
                type="checkbox"
                className="h-4 w-4 rounded border-gray-300"
                checked={search.replies !== false}
                onChange={e => patchFilters({ replies: e.target.checked ? undefined : false })}
              />
              Search replies
            </label>
          </div>
        </div>

        {/* My Group's Tickets, split by who personally holds the ticket. */}
        {view === 'groups' && (
          <div role="group" aria-label="Who holds the ticket" className="flex flex-wrap gap-2">
            {([undefined, ...HELD_BY] as const).map(h => (
              <button
                key={h ?? 'all'}
                type="button"
                aria-pressed={heldBy === h}
                onClick={() => update(s => ({ ...s, held_by: h, page: undefined }), true)}
                className={
                  'rounded-md border px-3 py-1.5 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 pointer-coarse:min-h-11 ' +
                  (heldBy === h
                    ? 'border-gray-900 bg-gray-900 text-white'
                    : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-50')
                }
              >
                {HELD_BY_LABEL[h ?? 'all']}
              </button>
            ))}
          </div>
        )}

        {jumpError && (
          <p role="alert" className="text-sm text-red-600">{jumpError}</p>
        )}

        {/* The desktop panel. Each change applies at once. */}
        {filtersOpen && wide && (
          <section aria-label="Filters" className="rounded-md border border-gray-200 bg-white p-4">
            <TicketFilters
              value={search}
              onChange={patchFilters}
              layout="panel"
              staff={isStaffOrAdmin}
              slaOn={slaOn}
              statuses={statuses}
              offered={offered}
            />
            <div className="mt-4 flex items-center justify-end gap-3 border-t border-gray-100 pt-3">
              <button
                type="button"
                onClick={() => update(s => clearAll(s), true)}
                className="rounded-sm text-xs text-gray-500 underline underline-offset-2 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 pointer-coarse:min-h-11 pointer-coarse:px-2"
              >
                Clear all
              </button>
              <Button type="button" variant="outline" size="sm" className="pointer-coarse:h-11" onClick={() => setFiltersOpen(false)}>
                Hide filters
              </Button>
            </div>
          </section>
        )}
        {!wide && (
          <FilterDialog
            open={filtersOpen}
            onOpenChange={setFiltersOpen}
            search={{ ...effective, q: undefined }}
            onApply={applyDraft}
            returnFocusTo={filtersButton}
            staff={isStaffOrAdmin}
            slaOn={slaOn}
            statuses={statuses}
            offered={offered}
          />
        )}

        {/* Active filters, each with its own ×. They replace the separate
            "Filtering by status" and "Filtering by client" rows. */}
        <FilterChips
          search={hideItem ? { ...search, item: undefined } : search}
          names={chipNames}
          staff={isStaffOrAdmin}
          offered={offered}
          onClear={k => update(s => clearFilter(s, k as FilterKey), true)}
          onClearAll={() => update(s => clearAll(s), true)}
        />

        {/* What the Search view covers (#444, #676). Always in the page, only its
            text changes: a live region added together with its text is often
            not announced. */}
        {isStaffOrAdmin && (
          <div className={searching ? 'flex items-center gap-2 text-sm' : 'sr-only'}>
            <span aria-live="polite" className="text-gray-500">
              {searching ? 'Searching all tickets you can see' : ''}
            </span>
          </div>
        )}

        {/* Bulk action bar — desktop-only, like the selection checkboxes it
            controls (see the table/card split below): below md there is
            nothing left to select, so a bar surviving a resize down would
            float with no controls under it and overflow the 390px card view
            besides. */}
        {someSelected && isStaffOrAdmin && (
          <div className="hidden space-y-2 md:block">
          <div className="flex items-center gap-3 rounded-md border border-blue-200 bg-blue-50 px-4 py-2 text-sm">
            <span className="text-blue-700 font-medium">{selectedIds.size} selected</span>
            <select
              className="rounded border border-gray-300 bg-white px-2 py-1 text-sm"
              value={bulkStatusId}
              onChange={e => setBulkStatusId(e.target.value)}
            >
              <option value="">Change status…</option>
              {statuses.map(s => (
                <option key={s.id} value={s.id}>{s.name}</option>
              ))}
            </select>
            <Button
              size="sm"
              disabled={!bulkStatusId || bulkMutation.isPending}
              onClick={() => bulkMutation.mutate(bulkStatusId)}
            >
              Apply
            </Button>
            <button
              type="button"
              className="ml-auto text-xs text-gray-500 hover:text-gray-700"
              onClick={() => { setSelectedIds(new Set()); setBulkError('') }}
            >
              Clear selection
            </button>
          </div>
          {bulkError && (
            <p role="alert" className="text-sm text-red-600">{bulkError}</p>
          )}
          </div>
        )}

        {/* What the server could not do for the replies, and what a link's SLA
            parts came to. Both are about this search only: the results are still
            right for the subject and the description. The regions are always in
            the page and only their text changes: a live region added together
            with its text is often not announced. */}
        <div role="status" aria-label="Reply search" className={replyNotice ? '' : 'sr-only'}>
          {replyNotice && (
            <p className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900">{replyNotice}</p>
          )}
        </div>
        <div role="status" aria-label="SLA in this link" className={slaLinkDropped ? '' : 'sr-only'}>
          {slaLinkDropped && (
            <p className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900">
              SLA tracking is off, so this link&apos;s SLA {search.sla && search.sort === 'sla' ? 'filter and sort were' : search.sla ? 'filter was' : 'sort was'} not
              applied.
            </p>
          )}
        </div>

        {/* Results */}
        {listError && !result ? (
          <p role="alert" className="text-sm text-red-600">
            {extractError(listError)}
          </p>
        ) : isFetching && !result ? (
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Spinner size="sm" /> Loading tickets…
          </div>
        ) : tickets.length === 0 ? (
          <div className="py-8 text-center text-sm text-gray-500">
            {filterCount > 0 ? (
              <>
                <p>No tickets match these filters.</p>
                <button
                  type="button"
                  onClick={() => update(s => clearAll(s), true)}
                  className="mt-2 rounded-sm underline underline-offset-2 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 pointer-coarse:min-h-11 pointer-coarse:px-2"
                >
                  Clear all
                </button>
              </>
            ) : (
              <p>{query.trim() ? 'No tickets match your search.' : emptyMessageFor(view, heldBy)}</p>
            )}
          </div>
        ) : (
          <>
            {/* How the list is ordered, and the way back to best match. */}
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-sm text-gray-500">
              <span aria-live="polite">
                {sortNow.sort === 'relevance'
                  ? 'Sorted by best match.'
                  : `Sorted by ${SORT_LABEL[sortNow.sort]}, ${orderWords(sortNow.sort, sortNow.order)}.`}
              </span>
              {effective.q && effective.sort && (
                <button
                  type="button"
                  onClick={() => update(s => ({ ...s, sort: undefined, order: undefined, page: undefined }))}
                  className="rounded-sm underline underline-offset-2 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 pointer-coarse:min-h-11 pointer-coarse:px-2"
                >
                  Sort by best match
                </button>
              )}
              {/* Created and Updated headings appear from xl and 2xl, where the
                  table has room for them, and below md it is cards with no
                  headings to click. This reaches every column in between. */}
              <div className="flex items-center gap-2 2xl:hidden">
                <label htmlFor="sort-by" className="text-gray-600">Sort by</label>
                <Select
                  id="sort-by"
                  className="h-9 w-auto pointer-coarse:h-11"
                  value={sortNow.sort}
                  onChange={e => {
                    if (e.target.value === 'relevance') update(s => ({ ...s, sort: undefined, order: undefined, page: undefined }))
                    else sortBy(e.target.value as SortKey)
                  }}
                >
                  {sortNow.sort === 'relevance' && <option value="relevance">Best match</option>}
                  {sortableKeys.map(k => (
                    <option key={k} value={k}>{SORT_LABEL[k]}</option>
                  ))}
                </Select>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="h-9 w-9 pointer-coarse:h-11 pointer-coarse:w-11"
                  disabled={sortNow.sort === 'relevance'}
                  aria-label={`Reverse the order (now ${orderWords(sortNow.sort, sortNow.order)})`}
                  onClick={() => sortNow.sort !== 'relevance' && sortBy(sortNow.sort)}
                >
                  {sortNow.order === 'asc' ? <ArrowUpIcon aria-hidden="true" className="h-4 w-4" /> : <ArrowDownIcon aria-hidden="true" className="h-4 w-4" />}
                </Button>
              </div>
            </div>

            {/* Desktop: the table, with the selection column bulk actions
                depend on. Bulk selection stays desktop-only (see DESIGN.md's
                Small screens section) — a checkbox per card plus a floating
                action bar is a different feature, not this one at a smaller
                size, so the card view below has no selection UI at all. */}
            <div className="hidden overflow-hidden rounded-md border border-gray-200 md:block">
              <table className="w-full text-sm">
                <thead className="bg-gray-50 text-xs font-medium uppercase tracking-wider text-gray-500">
                  <tr>
                    {isStaffOrAdmin && (
                      <th className="w-8 px-3 py-2">
                        <input
                          type="checkbox"
                          aria-label="Select every ticket on this page"
                          className="h-4 w-4 rounded border-gray-300"
                          checked={allSelected}
                          onChange={toggleAll}
                        />
                      </th>
                    )}
                    <SortHeader k="number" now={sortNow} staff={isStaffOrAdmin} onSort={sortBy} />
                    <SortHeader k="subject" now={sortNow} staff={isStaffOrAdmin} onSort={sortBy} />
                    <SortHeader k="status" now={sortNow} staff={isStaffOrAdmin} onSort={sortBy} />
                    <SortHeader k="priority" now={sortNow} staff={isStaffOrAdmin} onSort={sortBy} />
                    {showSLAColumn && <SortHeader k="sla" now={sortNow} staff={isStaffOrAdmin} onSort={sortBy} />}
                    <SortHeader k="created" now={sortNow} staff={isStaffOrAdmin} onSort={sortBy} tight className="hidden xl:table-cell" />
                    {isStaffOrAdmin && (
                      <SortHeader k="updated" now={sortNow} staff={isStaffOrAdmin} onSort={sortBy} tight className="hidden xl:table-cell" />
                    )}
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 bg-white">
                  {tickets.map(t => {
                    const status = statusFor(t.status_id)
                    return (
                      <tr
                        key={t.id}
                        className={`cursor-pointer hover:bg-gray-50 ${selectedIds.has(t.id) ? 'bg-blue-50' : ''}`}
                        onClick={() => navigate({ to: '/tickets/$id', params: { id: t.id } })}
                      >
                        {isStaffOrAdmin && (
                          <td className="w-8 px-3 py-2" onClick={e => e.stopPropagation()}>
                            <input
                              type="checkbox"
                              aria-label={`Select ticket ${t.tracking_number}`}
                              className="h-4 w-4 rounded border-gray-300"
                              checked={selectedIds.has(t.id)}
                              onChange={() => toggleOne(t.id)}
                            />
                          </td>
                        )}
                        <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-gray-500">
                          {t.tracking_number}
                        </td>
                        {/* max-w-[16rem] on both: the Link truncates, but a cell's narrowest width
                            is its text up to this cap, so a wider cap is room the status chip
                            (which never wraps) and the date columns would need. */}
                        <td className={'max-w-[16rem] px-3 py-2 font-medium text-gray-900 ' + (t.reply_match ? '' : 'truncate')}>
                          {/* A real link, not just a row click. The row was a
                              <tr onClick>, which no amount of tabbing reaches,
                              so a keyboard-only user had no way into a ticket
                              at all — and a reporting user had no way in by any
                              route, since "Jump to ticket" is staff-only.
                              Anchors also give middle-click and copy-link. */}
                          <Link
                            to="/tickets/$id"
                            params={{ id: t.id }}
                            className="block truncate rounded-sm text-inherit hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
                            title={t.subject}
                          >
                            {t.subject}
                          </Link>
                          {t.reply_match && <ReplyMatchLine match={t.reply_match} />}
                        </td>
                        <td className="px-3 py-2">
                          {status ? (
                            <span
                              // One line, and at most 7.5rem: status names are free text and the
                              // table's box clips instead of scrolling, so an unbounded chip
                              // pushes the date columns out of sight. "In Progress" is 93px.
                              // The text is all in the page (title as well); the cut is only
                              // what is drawn.
                              className="inline-flex max-w-[7.5rem] items-center gap-1 whitespace-nowrap rounded-full border px-2 py-0.5 text-xs font-medium"
                              style={{ borderColor: status.color, color: status.color }}
                            >
                              <span className="h-1.5 w-1.5 shrink-0 rounded-full" style={{ backgroundColor: status.color }} />
                              {/* The title is on the text, the element the ellipsis cuts. */}
                              <span className="min-w-0 truncate" title={status.name}>{status.name}</span>
                            </span>
                          ) : '—'}
                        </td>
                        <td className="px-3 py-2">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <Badge variant={priorityVariant(t.priority) as never}>
                              {t.priority}
                            </Badge>
                            {isStaffOrAdmin && <ReopenedBadge reopenCount={t.reopen_count} statusName={status?.name} />}
                            {isStaffOrAdmin && <QuarantinedBadge hasQuarantined={t.has_quarantined_attachment} />}
                          </div>
                        </td>
                        {showSLAColumn && (
                          <td className="px-3 py-2">
                            <div className="flex items-center gap-1.5">
                              <SLAIndicator sla={t.sla} compact />
                            </div>
                          </td>
                        )}
                        <td className="hidden whitespace-nowrap px-2 py-2 text-xs text-gray-500 xl:table-cell" title={new Date(t.created_at).toLocaleString()}>
                          {shortDate(t.created_at)}
                        </td>
                        {isStaffOrAdmin && (
                          <td className="hidden whitespace-nowrap px-2 py-2 text-xs text-gray-500 xl:table-cell" title={new Date(t.updated_at ?? t.created_at).toLocaleString()}>
                            {t.updated_at ? shortDate(t.updated_at) : ''}
                          </td>
                        )}
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>

            {/* Mobile: one card per ticket, the same table shrunk into a row
                is not a mobile layout — it is the same table with less room.
                The subject stays the anchor #9ff0e9a added: the card is a
                link, not a click handler, so a keyboard or reporting user
                reaches it the same way. */}
            <ul className="space-y-2 md:hidden">
              {tickets.map(t => {
                const status = statusFor(t.status_id)
                return (
                  <li key={t.id} className="rounded-md border border-gray-200 bg-white p-3">
                    <Link
                      to="/tickets/$id"
                      params={{ id: t.id }}
                      className="block rounded-sm text-inherit focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="font-mono text-xs text-gray-500">{t.tracking_number}</span>
                        {status ? (
                          <span
                            className="inline-flex min-w-0 items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium"
                            style={{ borderColor: status.color, color: status.color }}
                          >
                            <span className="h-1.5 w-1.5 shrink-0 rounded-full" style={{ backgroundColor: status.color }} />
                            <span className="min-w-0 wrap-anywhere">{status.name}</span>
                          </span>
                        ) : '—'}
                      </div>
                      <p className="mt-1.5 wrap-anywhere font-medium text-gray-900">{t.subject}</p>
                      {t.reply_match && <ReplyMatchLine match={t.reply_match} />}
                      <div className="mt-1.5 flex flex-wrap items-center gap-2">
                        <Badge variant={priorityVariant(t.priority) as never}>
                          {t.priority}
                        </Badge>
                        {isStaffOrAdmin && <ReopenedBadge reopenCount={t.reopen_count} statusName={status?.name} />}
                        {isStaffOrAdmin && <QuarantinedBadge hasQuarantined={t.has_quarantined_attachment} />}
                        {showSLAColumn && <SLAIndicator sla={t.sla} compact />}
                        {/* Last, and pushed right, so on a narrow card it wraps to its own
                            line rather than crowding the badges. Staff only, like the Updated
                            sort and column: it is what the Sort by menu orders by. */}
                        {isStaffOrAdmin && t.updated_at && (
                          <span className="ml-auto whitespace-nowrap text-xs text-gray-500">Updated {shortDate(t.updated_at)}</span>
                        )}
                      </div>
                    </Link>
                  </li>
                )
              })}
            </ul>
          </>
        )}

        {/* Shown whenever there is more than one page to move between. The
            server returns an array rather than a total, so there is no page
            count to display — only whether moving is possible. */}
        {(page > 0 || mayHaveMore) && (
          <div className="flex items-center justify-between pt-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => goPage(page)}
              disabled={page === 0 || isFetching}
            >
              Previous
            </Button>
            <span className="text-sm text-gray-500" aria-live="polite">
              {`Page ${page + 1}`}
            </span>
            <Button
              variant="outline"
              size="sm"
              onClick={() => goPage(page + 2)}
              disabled={!mayHaveMore || isFetching}
            >
              Next
            </Button>
          </div>
        )}
      </div>
    </Layout>
  )
}
