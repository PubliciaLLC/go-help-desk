import { useState, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { listTickets, getTicket } from '@/api/tickets'
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

export function TicketListPage() {
  const navigate = useNavigate()
  const { user } = useAuthStore()
  const isStaffOrAdmin = user?.role === 'staff' || user?.role === 'admin'

  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [lookupError, setLookupError] = useState('')

  // 300 ms debounce
  useEffect(() => {
    const id = setTimeout(() => setDebouncedQuery(query), 300)
    return () => clearTimeout(id)
  }, [query])

  const { data: statuses = [] } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

  const { data: tickets = [], isFetching } = useQuery({
    queryKey: ['tickets', { q: debouncedQuery }],
    queryFn: () => listTickets({ q: debouncedQuery || undefined }),
    enabled: debouncedQuery.length >= 2,
  })

  // Direct tracking-number / UUID jump (form submit)
  async function handleLookup(e: React.FormEvent) {
    e.preventDefault()
    const q = query.trim()
    if (!q) return
    setLookupError('')
    try {
      const t = await getTicket(q)
      navigate({ to: '/tickets/$id', params: { id: t.id } })
    } catch {
      setLookupError(`No ticket found for "${q}"`)
    }
  }

  function statusFor(id: string) {
    return statuses.find((s) => s.id === id)
  }

  const showResults = debouncedQuery.length >= 2

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

        {/* Search bar */}
        <form onSubmit={handleLookup} className="flex gap-2">
          <div className="relative flex-1 max-w-lg">
            <SearchIcon className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" />
            <Input
              className="pl-9"
              placeholder="Search by tracking number, subject, or description…"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value)
                setLookupError('')
              }}
            />
          </div>
          {isStaffOrAdmin && (
            <Button type="submit" variant="outline">Jump to ticket</Button>
          )}
        </form>
        {lookupError && <p className="text-sm text-red-600">{lookupError}</p>}

        {/* Live results */}
        {showResults ? (
          <div className="space-y-2">
            <div className="flex items-center gap-2 text-sm text-gray-500">
              {isFetching
                ? <><Spinner size="sm" /> Searching…</>
                : <span>{tickets.length} result{tickets.length !== 1 ? 's' : ''}</span>}
            </div>

            {!isFetching && tickets.length === 0 && (
              <p className="text-sm text-gray-500">No tickets match your search.</p>
            )}

            {tickets.length > 0 && (
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
                    {tickets.map((t) => {
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
        ) : (
          /* Idle state — status legend */
          statuses.length > 0 && (
            <div className="space-y-3">
              <p className="text-sm text-gray-500">
                Type at least 2 characters to search across ticket numbers, subjects, and descriptions.
              </p>
              <div className="flex flex-wrap gap-2">
                {statuses.map((s) => (
                  <span
                    key={s.id}
                    className="inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium"
                    style={{ borderColor: s.color, color: s.color }}
                  >
                    <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: s.color }} />
                    {s.name}
                  </span>
                ))}
              </div>
            </div>
          )
        )}
      </div>
    </Layout>
  )
}
