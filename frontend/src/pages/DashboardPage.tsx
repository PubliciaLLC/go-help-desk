import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useAuthStore } from '@/store/auth'
import { listStatuses } from '@/api/admin'
import { Layout } from '@/components/Layout'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon } from 'lucide-react'

export function DashboardPage() {
  const { user } = useAuthStore()
  const { data: statuses, isLoading } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

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
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
            {statuses?.map((s) => (
              <Card key={s.id}>
                <CardHeader className="pb-2">
                  <CardTitle className="text-sm font-medium text-gray-500">{s.name}</CardTitle>
                </CardHeader>
                <CardContent>
                  <div
                    className="h-3 w-3 rounded-full"
                    style={{ backgroundColor: s.color }}
                  />
                </CardContent>
              </Card>
            ))}
          </div>
        )}

        <div className="flex gap-4">
          <Link to="/tickets">
            <Button variant="outline">View all tickets</Button>
          </Link>
        </div>
      </div>
    </Layout>
  )
}
