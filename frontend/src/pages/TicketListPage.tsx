import { useState, useEffect, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { getTicket, listTickets, updateTicket, type TicketScope } from '@/api/tickets'
import { listStatuses, listUsers } from '@/api/admin'
import { useAuthStore } from '@/store/auth'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon, SearchIcon } from 'lucide-react'
import { priorityVariant } from '@/lib/format'
import { SLAIndicator } from '@/components/ticket/SLAIndicator'
import { apiRefusal, extractError } from '@/api/client'

function emptyMessageFor(scope: TicketScope) {
  switch (scope) {
    case 'unassigned':
      return 'No unassigned tickets.'
    case 'all':
      return 'No tickets in the system.'
    default:
      return 'No tickets are currently assigned to you or your groups.'
  }
}

// Matches the server's default. Larger pages mean fewer clicks; smaller ones
// mean a faster first paint. 50 is the compromise.
const PAGE_SIZE = 50

export function TicketListPage() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { status: statusFilter, reporter: reporterFilter } = useSearch({ from: '/tickets' })
  const { user } = useAuthStore()
  const isStaffOrAdmin = user?.role === 'staff' || user?.role === 'admin'
  const isAdmin = user?.role === 'admin'

  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [includeClosed, setIncludeClosed] = useState(false)
  const [scope, setScope] = useState<TicketScope>('mine')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [bulkStatusId, setBulkStatusId] = useState('')
  const [bulkError, setBulkError] = useState('')
  const [jumpError, setJumpError] = useState('')

  // 300 ms debounce on the search box
  useEffect(() => {
    const id = setTimeout(() => setDebouncedQuery(query), 300)
    return () => clearTimeout(id)
  }, [query])

  const { data: statuses = [] } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

  const { data: users = [] } = useQuery({
    queryKey: ['users'],
    queryFn: () => listUsers(),
    enabled: isAdmin,
  })

  // Non-admins are always scoped to "mine" — the backend rejects other scopes.
  const effectiveScope: TicketScope = isAdmin ? scope : 'mine'

  // The server returns at most PAGE_SIZE rows. Before paging existed it
  // returned at most 100 and there was no way to ask for the rest, so anything
  // older than the hundredth ticket was unreachable by browsing.
  //
  // The page number is stored together with WHAT it is a page of. Any change
  // to what is being asked for starts again at the first page — otherwise a
  // narrower search lands on an offset past its own results and shows an
  // empty list. Kept as one piece of state rather than a number plus an
  // effect that resets it, because the effect ran a render late: the query
  // fired once for the new search at the old offset, and that wasted request
  // is the empty flash it was meant to prevent.
  const asking = `${debouncedQuery}\u0000${effectiveScope}\u0000${reporterFilter ?? ''}`
  const [paged, setPaged] = useState({ asking, page: 0 })
  const page = paged.asking === asking ? paged.page : 0
  const setPage = (next: (p: number) => number) =>
    setPaged({ asking, page: next(page) })

  const { data: allTickets = [], isFetching } = useQuery({
    queryKey: ['tickets', { q: debouncedQuery || undefined, scope: effectiveScope, reporter: reporterFilter, page }],
    queryFn: () =>
      listTickets({
        q: debouncedQuery || undefined,
        scope: reporterFilter ? undefined : effectiveScope,
        reporter_id: reporterFilter,
        limit: PAGE_SIZE,
        offset: page * PAGE_SIZE,
      }),
    // Keeping the previous page visible while the next loads stops the table
    // collapsing to empty on every click.
    placeholderData: (previous) => previous,
    // Re-polls only once this page has actually shown a live SLA status — an
    // instance with SLA tracking off, or a page of tickets with no matching
    // policy, never polls for it. See TicketDetailPage for the same choice.
    refetchInterval: (query) => (query.state.data?.some(t => t.sla != null) ? 60_000 : false),
  })

  // A full page means there is probably another. The server returns an array,
  // not a total, so this is what there is to go on — and it is enough for
  // next/previous.
  const mayHaveMore = allTickets.length === PAGE_SIZE

  // IDs of statuses named "Closed" — filtered out unless the toggle is on.
  const closedIds = useMemo(
    () => new Set(statuses.filter(s => s.name === 'Closed').map(s => s.id)),
    [statuses],
  )

  const tickets = useMemo(() => {
    let list = includeClosed ? allTickets : allTickets.filter(t => !closedIds.has(t.status_id))
    if (statusFilter) list = list.filter(t => t.status_id === statusFilter)
    return list
  }, [allTickets, includeClosed, closedIds, statusFilter])

  // Shown only when at least one row actually has a status to show — an
  // instance with SLA tracking off, or a reporter whose tickets carry no
  // policy, sees the same table it saw before this column existed.
  const showSLAColumn = useMemo(() => tickets.some(t => t.sla != null), [tickets])

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
          <h1 className="text-2xl font-bold text-gray-900">Tickets</h1>
          <Link to="/tickets/new">
            <Button>
              <PlusIcon className="mr-2 h-4 w-4" />
              New Ticket
            </Button>
          </Link>
        </div>

        {/* Toolbar: search + scope + closed toggle */}
        <div className="flex items-center gap-3 flex-wrap">
          <div className="relative flex-1 max-w-lg">
            <SearchIcon className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" />
            <Input
              className="pl-9"
              placeholder="Search by tracking number, subject, or description…"
              value={query}
              onChange={e => setQuery(e.target.value)}
            />
          </div>
          {isAdmin && (
            <div className="inline-flex rounded-md border border-gray-200 overflow-hidden text-sm">
              {(['mine', 'unassigned', 'all'] as const).map(s => (
                <button
                  key={s}
                  type="button"
                  onClick={() => setScope(s)}
                  className={
                    'px-3 py-1.5 capitalize transition-colors ' +
                    (scope === s
                      ? 'bg-gray-900 text-white'
                      : 'bg-white text-gray-700 hover:bg-gray-50')
                  }
                >
                  {s}
                </button>
              ))}
            </div>
          )}
          <label className="flex items-center gap-2 text-sm text-gray-600 cursor-pointer select-none whitespace-nowrap">
            <input
              type="checkbox"
              className="h-4 w-4 rounded border-gray-300"
              checked={includeClosed}
              onChange={e => setIncludeClosed(e.target.checked)}
            />
            Include closed
          </label>
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
        </div>

        {jumpError && (
          <p role="alert" className="text-sm text-red-600">{jumpError}</p>
        )}

        {/* Active status filter chip */}
        {statusFilter && (() => {
          const s = statuses.find(st => st.id === statusFilter)
          return s ? (
            <div className="flex items-center gap-2 text-sm">
              <span className="text-gray-500">Filtering by status:</span>
              <span
                className="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-medium"
                style={{ borderColor: s.color, color: s.color }}
              >
                <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: s.color }} />
                {s.name}
              </span>
              <Link to="/tickets" search={{ status: undefined, reporter: undefined }} className="text-xs text-gray-400 hover:text-gray-600">
                Clear ×
              </Link>
            </div>
          ) : null
        })()}

        {/* Reporter (client) filter chip */}
        {reporterFilter && (() => {
          const u = users.find(u => u.id === reporterFilter)
          return (
            <div className="flex items-center gap-2 text-sm">
              <span className="text-gray-500">Filtering by client:</span>
              <span className="inline-flex items-center gap-1.5 rounded-full border border-gray-300 bg-gray-100 px-2.5 py-0.5 text-xs font-medium text-gray-700">
                {u?.display_name ?? reporterFilter}
              </span>
              <Link to="/tickets" search={{ status: undefined, reporter: undefined }} className="text-xs text-gray-400 hover:text-gray-600">
                Clear ×
              </Link>
            </div>
          )
        })()}

        {/* Bulk action bar */}
        {someSelected && isStaffOrAdmin && (
          <div className="space-y-2">
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

        {/* Results */}
        {isFetching && allTickets.length === 0 ? (
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Spinner size="sm" /> Loading tickets…
          </div>
        ) : tickets.length === 0 ? (
          <p className="text-sm text-gray-500 py-8 text-center">
            {query
              ? 'No tickets match your search.'
              : emptyMessageFor(effectiveScope)}
          </p>
        ) : (
          <>
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
                    <th className="px-4 py-2 text-left">Ticket</th>
                    <th className="px-4 py-2 text-left">Subject</th>
                    <th className="px-4 py-2 text-left">Status</th>
                    <th className="px-4 py-2 text-left">Priority</th>
                    {showSLAColumn && <th className="px-4 py-2 text-left">SLA</th>}
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
                        <td className="whitespace-nowrap px-4 py-2 font-mono text-xs text-gray-500">
                          {t.tracking_number}
                        </td>
                        <td className="px-4 py-2 font-medium text-gray-900 max-w-xs truncate">
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
                          >
                            {t.subject}
                          </Link>
                        </td>
                        <td className="px-4 py-2">
                          {status ? (
                            <span
                              className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium"
                              style={{ borderColor: status.color, color: status.color }}
                            >
                              <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: status.color }} />
                              {status.name}
                            </span>
                          ) : '—'}
                        </td>
                        <td className="px-4 py-2">
                          <Badge variant={priorityVariant(t.priority) as never}>
                            {t.priority}
                          </Badge>
                        </td>
                        {showSLAColumn && (
                          <td className="px-4 py-2">
                            <div className="flex items-center gap-1.5">
                              <SLAIndicator sla={t.sla} compact />
                            </div>
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
                            className="inline-flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium"
                            style={{ borderColor: status.color, color: status.color }}
                          >
                            <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: status.color }} />
                            {status.name}
                          </span>
                        ) : '—'}
                      </div>
                      <p className="mt-1.5 font-medium text-gray-900">{t.subject}</p>
                      <div className="mt-1.5 flex items-center gap-2">
                        <Badge variant={priorityVariant(t.priority) as never}>
                          {t.priority}
                        </Badge>
                        {showSLAColumn && <SLAIndicator sla={t.sla} compact />}
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
              onClick={() => setPage((p) => Math.max(0, p - 1))}
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
              onClick={() => setPage((p) => p + 1)}
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
