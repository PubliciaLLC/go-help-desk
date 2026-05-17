import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useAuthStore } from '@/store/auth'
import { listStatuses, listUsers } from '@/api/admin'
import { Layout } from '@/components/Layout'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon, UserIcon } from 'lucide-react'

export function DashboardPage() {
  const { user } = useAuthStore()
  const isAdmin = user?.role === 'admin'

  const { data: statuses, isLoading } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

  const { data: allUsers = [] } = useQuery({
    queryKey: ['users'],
    queryFn: () => listUsers(),
    enabled: isAdmin,
  })

  const clients = allUsers.filter(u => u.role === 'user' && !u.disabled)

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900">Dashboard</h1>
            <p className="text-sm text-gray-500">Welcome back, {user?.display_name}</p>
          </div>
          <Link to="/tickets/new">
            <Button>
              <PlusIcon className="mr-2 h-4 w-4" />
              New Ticket
            </Button>
          </Link>
        </div>

        {isLoading ? (
          <div className="flex justify-center py-12">
            <Spinner />
          </div>
        ) : (
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
            {statuses?.filter((s) => s.active).map((s) => (
              <Link key={s.id} to="/tickets" search={{ status: s.id }} className="block group">
                <Card className="border-l-4 transition-shadow group-hover:shadow-md cursor-pointer" style={{ borderLeftColor: s.color }}>
                  <CardHeader className="pb-1">
                    <CardTitle className="text-sm font-medium text-gray-500">{s.name}</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <p className="text-3xl font-bold text-gray-900">{s.ticket_count}</p>
                  </CardContent>
                </Card>
              </Link>
            ))}
          </div>
        )}

        <div className="flex gap-4">
          <Link to="/tickets" search={{ status: undefined }}>
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
                        <p className="truncate text-sm font-medium text-gray-900">{c.display_name}</p>
                        <p className="truncate text-xs text-gray-500">{c.email}</p>
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
