import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useAuthStore } from '@/store/auth'
import { listStatuses, listUsers } from '@/api/admin'
import { getDashboard } from '@/api/dashboard'
import { Layout } from '@/components/Layout'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { AdminHealthCard } from '@/components/dashboard/AdminHealthCard'
import { QueueStrip } from '@/components/dashboard/QueueStrip'
import { NeedsAttention } from '@/components/dashboard/NeedsAttention'
import { VolumeTrend } from '@/components/charts'
import { PlusIcon, UserIcon } from 'lucide-react'

// The trend counts its days in the viewer's own zone, so a day is a calendar day
// where they are.
const browserTZ = () => Intl.DateTimeFormat().resolvedOptions().timeZone

// What a block shows while the summary loads: a spinner in a box of about the
// block's own height, so the page does not jump when the numbers arrive.
function Loading({ testId, className }: { testId: string; className: string }) {
  return (
    <div data-testid={testId} className={`flex items-center justify-center ${className}`}>
      <Spinner />
    </div>
  )
}

function Block({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="min-w-0 space-y-3 rounded-lg border bg-card p-4 shadow-sm">
      <h2 className="text-base font-semibold text-gray-900">{title}</h2>
      {children}
    </section>
  )
}

export function DashboardPage() {
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'admin'
  const requester = user?.role === 'user'

  const { data: statuses, isLoading } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

  const { data: allUsers = [] } = useQuery({
    queryKey: ['users'],
    queryFn: () => listUsers(),
    enabled: isAdmin,
  })

  // One request feeds the strip, the attention list and the trend. Kept for a
  // minute; a return to the tab refreshes it once it is older than that.
  const tz = browserTZ()
  const summary = useQuery({
    queryKey: ['dashboard', tz],
    queryFn: () => getDashboard(tz),
    staleTime: 60_000,
    refetchOnWindowFocus: true,
  })

  const clients = allUsers.filter(u => u.role === 'user' && !u.disabled)

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900">Dashboard</h1>
            <p className="text-sm text-gray-500 wrap-anywhere">Welcome back, {user?.display_name}</p>
          </div>
          <Link to="/tickets/new">
            <Button>
              <PlusIcon className="mr-2 h-4 w-4" />
              New Ticket
            </Button>
          </Link>
        </div>

        {isAdmin && <AdminHealthCard />}

        {summary.isError ? (
          <div role="alert" className="flex flex-wrap items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">
            <span>The dashboard summary could not be loaded.</span>
            <Button variant="outline" size="sm" onClick={() => void summary.refetch()} disabled={summary.isFetching}>
              Retry
            </Button>
          </div>
        ) : summary.data ? (
          <QueueStrip queues={summary.data.queues ?? []} requester={requester} />
        ) : (
          <section className="space-y-2">
            <h2 className="text-base font-semibold text-gray-900">{requester ? 'Your tickets' : 'Your queue'}</h2>
            <Loading testId="dashboard-strip-loading" className="min-h-[5rem]" />
          </section>
        )}

        {isLoading ? (
          <div className="flex justify-center py-12">
            <Spinner />
          </div>
        ) : (
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
            {statuses?.filter((s) => s.active).map((s) => (
              <Link key={s.id} to="/tickets" search={{ status: [s.id], reporter: undefined }} className="block group">
                <Card className="border-l-4 transition-shadow group-hover:shadow-md cursor-pointer" style={{ borderLeftColor: s.color }}>
                  <CardHeader className="pb-1">
                    <CardTitle className="text-sm font-medium text-gray-500 [overflow-wrap:anywhere]">{s.name}</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <p className="text-3xl font-bold text-gray-900">{s.ticket_count}</p>
                  </CardContent>
                </Card>
              </Link>
            ))}
          </div>
        )}

        {!requester && !summary.isError && (
          <div className="grid gap-4 lg:grid-cols-2">
            <Block title="Needs attention">
              {summary.data ? (
                <NeedsAttention items={summary.data.attention ?? []} statuses={statuses} />
              ) : (
                <Loading testId="dashboard-attention-loading" className="min-h-[14rem]" />
              )}
            </Block>
            <Block title="Tickets in the last 30 days">
              {summary.data ? (
                summary.data.volume && <VolumeTrend volume={summary.data.volume} />
              ) : (
                <Loading testId="dashboard-trend-loading" className="min-h-[14rem]" />
              )}
            </Block>
          </div>
        )}

        <div className="flex gap-4">
          <Link to="/tickets" search={{ status: undefined, reporter: undefined }}>
            <Button variant="outline">View all tickets</Button>
          </Link>
        </div>

        {isAdmin && clients.length > 0 && (
          <div className="space-y-3">
            <h2 className="text-base font-semibold text-gray-900">By Client</h2>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
              {clients.map(c => (
                <Link key={c.id} to="/tickets" search={{ reporter: c.id, status: undefined }} className="block group">
                  <Card className="transition-shadow group-hover:shadow-md cursor-pointer">
                    <CardContent className="flex items-center gap-3 py-3 px-4">
                      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-gray-100 text-gray-500">
                        <UserIcon className="h-4 w-4" />
                      </div>
                      <div className="min-w-0">
                        <p className="truncate text-sm font-medium text-gray-900" title={c.display_name}>{c.display_name}</p>
                        <p className="truncate text-xs text-gray-500" title={c.email}>{c.email}</p>
                      </div>
                    </CardContent>
                  </Card>
                </Link>
              ))}
            </div>
          </div>
        )}
      </div>
    </Layout>
  )
}
