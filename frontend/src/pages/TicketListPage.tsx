import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { getTicket } from '@/api/tickets'
import { listStatuses } from '@/api/admin'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PlusIcon, SearchIcon } from 'lucide-react'

export function TicketListPage() {
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [lookupError, setLookupError] = useState('')

  const { data: statuses = [] } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

  async function handleSearch(e: React.FormEvent) {
    e.preventDefault()
    const q = search.trim()
    if (!q) return
    setLookupError('')
    try {
      const t = await getTicket(q)
      navigate({ to: '/tickets/$id', params: { id: t.id } })
    } catch {
      setLookupError(`No ticket found for "${q}"`)
    }
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

        {/* Quick lookup by ID or tracking number */}
        <form onSubmit={handleSearch} className="flex gap-2">
          <div className="relative flex-1 max-w-sm">
            <SearchIcon className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" />
            <Input
              className="pl-9"
              placeholder="Ticket ID or OHD-2024-000001…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Button type="submit" variant="outline">Look up</Button>
        </form>
        {lookupError && <p className="text-sm text-red-600">{lookupError}</p>}

        {/* Status filter shortcuts */}
        {statuses.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {statuses.map((s) => (
              <span
                key={s.id}
                className="inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium cursor-default"
                style={{ borderColor: s.color, color: s.color }}
              >
                <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: s.color }} />
                {s.name}
              </span>
            ))}
          </div>
        )}

        <p className="text-sm text-gray-500">
          Use the search bar above to look up a specific ticket by ID or tracking number.
          Ticket list pagination will be added in a future update.
        </p>
      </div>
    </Layout>
  )
}
