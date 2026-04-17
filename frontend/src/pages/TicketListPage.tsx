import { useState, useEffect, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { listTickets, type TicketScope } from '@/api/tickets'
import { listStatuses } from '@/api/admin'
import { useAuthStore } from '@/store/auth'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon, SearchIcon } from 'lucide-react'

function priorityVariant(p: string) {
  if (p === 'critical') return 'destructive'
  if (p === 'high') return 'warning'
  if (p === 'medium') return 'default'
  return 'secondary'
}

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

export function TicketListPage() {
  const navigate = useNavigate()
  const { user } = useAuthStore()
  const isStaffOrAdmin = user?.role === 'staff' || user?.role === 'admin'
  const isAdmin = user?.role === 'admin'

  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [includeClosed, setIncludeClosed] = useState(false)
  const [scope, setScope] = useState<TicketScope>('mine')

  // 300 ms debounce on the search box
  useEffect(() => {
    const id = setTimeout(() => setDebouncedQuery(query), 300)
    return () => clearTimeout(id)
  }, [query])

  const { data: statuses = [] } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

  // Non-admins are always scoped to "mine" — the backend rejects other scopes.
  const effectiveScope: TicketScope = isAdmin ? scope : 'mine'

  // Always fetch; pass search query to backend when present.
  const { data: allTickets = [], isFetching } = useQuery({
    queryKey: ['tickets', { q: debouncedQuery || undefined, scope: effectiveScope }],
    queryFn: () =>
      listTickets({
        q: debouncedQuery || undefined,
        scope: effectiveScope,
      }),
  })

  // IDs of statuses named "Closed" — filtered out unless the toggle is on.
  const closedIds = useMemo(
    () => new Set(statuses.filter(s => s.name === 'Closed').map(s => s.id)),
    [statuses],
  )

  const tickets = useMemo(
    () => includeClosed ? allTickets : allTickets.filter(t => !closedIds.has(t.status_id)),
    [allTickets, includeClosed, closedIds],
  )

  function statusFor(id: string) {
    return statuses.find(s => s.id === id)
  }

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
                try {
                  const { getTicket } = await import('@/api/tickets')
                  const t = await getTicket(q)
                  navigate({ to: '/tickets/$id', params: { id: t.id } })
                } catch {
                  // not a valid tracking number / UUID — fall through to search results
                }
              }}
            >
              Jump to ticket
            </Button>
          )}
        </div>

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
          <div className="overflow-hidden rounded-md border border-gray-200">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs font-medium uppercase tracking-wider text-gray-500">
                <tr>
                  <th className="px-4 py-2 text-left">Ticket</th>
                  <th className="px-4 py-2 text-left">Subject</th>
                  <th className="px-4 py-2 text-left">Status</th>
                  <th className="px-4 py-2 text-left">Priority</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 bg-white">
                {tickets.map(t => {
                  const status = statusFor(t.status_id)
                  return (
                    <tr
                      key={t.id}
                      className="cursor-pointer hover:bg-gray-50"
                      onClick={() => navigate({ to: '/tickets/$id', params: { id: t.id } })}
                    >
                      <td className="whitespace-nowrap px-4 py-2 font-mono text-xs text-gray-500">
                        {t.tracking_number}
                      </td>
                      <td className="px-4 py-2 font-medium text-gray-900 max-w-xs truncate">
                        {t.subject}
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
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Layout>
  )
}
