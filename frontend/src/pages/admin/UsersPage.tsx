import { useState } from 'react'
import { authTypeLabel } from '@/lib/authType'
import { useQuery, useMutation, useQueryClient, keepPreviousData } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { listUsersPage, createUser, USERS_PER_PAGE } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { PlusIcon, SearchIcon } from 'lucide-react'
import type { Role } from '@/api/types'

const ROLES: Role[] = ['admin', 'staff', 'user']

type Show = 'all' | 'people' | 'service'

function roleBadge(role: Role) {
  if (role === 'admin') return 'destructive'
  if (role === 'staff') return 'default'
  return 'secondary'
}

export function UsersPage() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [email, setEmail] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [role, setRole] = useState<Role>('user')
  const [password, setPassword] = useState('')
  const [formError, setFormError] = useState('')
  const [show, setShow] = useState<Show>('all')

  // The box is a draft until submitted, so each keystroke is not a request.
  const [searchDraft, setSearchDraft] = useState('')
  const [search, setSearch] = useState('')
  const [roleFilter, setRoleFilter] = useState<Role | ''>('')
  const [page, setPage] = useState(1)

  const { data, isLoading } = useQuery({
    queryKey: ['admin', 'users', { page, search, roleFilter, show }],
    queryFn: () => listUsersPage(page, {
      q: search,
      role: roleFilter,
      serviceAccount: show === 'all' ? undefined : show === 'service',
    }),
    // Keep the old rows on screen while the next page loads rather than
    // flashing a spinner between every click.
    placeholderData: keepPreviousData,
  })
  const users = data?.users ?? []
  const total = data?.total ?? 0
  const firstShown = (page - 1) * USERS_PER_PAGE + 1
  const lastShown = (page - 1) * USERS_PER_PAGE + users.length

  const createMutation = useMutation({
    mutationFn: () => createUser({ email, display_name: displayName, role, password }),
    onSuccess: () => {
      setShowCreate(false)
      setEmail('')
      setDisplayName('')
      setRole('user')
      setPassword('')
      setFormError('')
      qc.invalidateQueries({ queryKey: ['admin', 'users'] })
    },
    onError: (err) => setFormError(extractError(err)),
  })

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <h1 className="text-2xl font-bold text-gray-900">Users</h1>
            <p className="mt-1 text-sm text-gray-500">Click a user to edit their profile, groups, and account settings.</p>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <Select
              aria-label="Show"
              value={show}
              onChange={(e) => {
                setShow(e.target.value as Show)
                setPage(1)
              }}
              className="w-44"
            >
              <option value="all">All accounts</option>
              <option value="people">People</option>
              <option value="service">Service accounts</option>
            </Select>
            <Button onClick={() => setShowCreate(!showCreate)}>
              <PlusIcon className="mr-2 h-4 w-4" />
              Add User
            </Button>
          </div>
        </div>

        {showCreate && (
          <Card>
            <CardHeader>
              <CardTitle className="text-base">New user</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-1">
                  <Label>Email</Label>
                  <Input value={email} onChange={(e) => setEmail(e.target.value)} type="email" />
                </div>
                <div className="space-y-1">
                  <Label>Display name</Label>
                  <Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
                </div>
                <div className="space-y-1">
                  <Label>Role</Label>
                  <Select value={role} onChange={(e) => setRole(e.target.value as Role)}>
                    {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
                  </Select>
                </div>
                <div className="space-y-1">
                  <Label>Password</Label>
                  <Input value={password} onChange={(e) => setPassword(e.target.value)} type="password" />
                </div>
              </div>
              {formError && <p role="alert" className="mt-2 text-sm text-red-600">{formError}</p>}
              <div className="mt-4 flex gap-2">
                <Button onClick={() => createMutation.mutate()} disabled={createMutation.isPending}>
                  {createMutation.isPending ? 'Creating…' : 'Create'}
                </Button>
                <Button variant="outline" onClick={() => setShowCreate(false)}>Cancel</Button>
              </div>
            </CardContent>
          </Card>
        )}

        <form
          role="search"
          className="flex flex-wrap items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            setSearch(searchDraft.trim())
            setPage(1)
          }}
        >
          <Input
            type="search"
            aria-label="Search users"
            placeholder="Search name or email"
            value={searchDraft}
            onChange={(e) => setSearchDraft(e.target.value)}
            className="max-w-xs"
          />
          <Button type="submit" variant="outline">
            <SearchIcon className="mr-2 h-4 w-4" />
            Search
          </Button>
          <Select
            aria-label="Filter by role"
            value={roleFilter}
            onChange={(e) => {
              setRoleFilter(e.target.value as Role | '')
              setPage(1)
            }}
            className="w-40"
          >
            <option value="">All roles</option>
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </Select>
        </form>

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="rounded-lg border bg-white overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
                <tr>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Email</th>
                  <th className="px-4 py-3 text-left">Role</th>
                  <th className="px-4 py-3 text-left">Login</th>
                  <th className="px-4 py-3 text-left">Joined</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {users.map((u) => (
                  <tr
                    key={u.id}
                    className={`cursor-pointer hover:bg-blue-50 transition-colors ${u.disabled ? 'opacity-60' : ''}`}
                    onClick={() => navigate({ to: '/admin/users/$id', params: { id: u.id } })}
                  >
                    <td className="px-4 py-3">
                      <span className="font-medium text-gray-900 wrap-anywhere">{u.display_name}</span>
                      {u.service_account && (
                        <Badge variant="secondary" className="ml-2 text-[10px]">Service</Badge>
                      )}
                      {u.disabled && (
                        <Badge variant="secondary" className="ml-2 text-[10px]">Disabled</Badge>
                      )}
                    </td>
                    <td className="px-4 py-3 text-gray-600 wrap-anywhere">{u.email}</td>
                    <td className="px-4 py-3">
                      <Badge variant={roleBadge(u.role) as never}>{u.role}</Badge>
                    </td>
                    <td className="px-4 py-3 text-gray-500 text-xs">
                      {authTypeLabel(u.auth_type)}
                    </td>
                    <td className="px-4 py-3 text-gray-500">{new Date(u.created_at).toLocaleDateString()}</td>
                  </tr>
                ))}
                {users.length === 0 && (
                  <tr><td colSpan={5} className="px-4 py-8 text-center text-gray-400">No users found.</td></tr>
                )}
              </tbody>
            </table>
          </div>
        )}

        {total > 0 && (
          <div className="flex items-center justify-between text-sm text-gray-600">
            <span>Showing {firstShown}-{lastShown} of {total}</span>
            <div className="flex gap-2">
              <Button variant="outline" disabled={page <= 1} onClick={() => setPage(page - 1)}>Previous</Button>
              <Button variant="outline" disabled={lastShown >= total} onClick={() => setPage(page + 1)}>Next</Button>
            </div>
          </div>
        )}
      </div>
    </Layout>
  )
}
