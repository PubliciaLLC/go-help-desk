import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { listUsers, createUser } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { PlusIcon } from 'lucide-react'
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

  const { data: users = [], isLoading } = useQuery({
    queryKey: ['admin', 'users'],
    queryFn: () => listUsers(),
  })

  // Client-side: GET /admin/users takes limit/offset only, so this filters the
  // page that was loaded. When paging (#467) lands this needs a server-side
  // parameter, or a service user past the first page is invisible to it.
  const shown = users.filter((u) =>
    show === 'all' ? true : show === 'service' ? !!u.service_account : !u.service_account,
  )

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
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900">Users</h1>
            <p className="mt-1 text-sm text-gray-500">Click a user to edit their profile, groups, and account settings.</p>
          </div>
          <div className="flex items-center gap-3">
            <Select
              aria-label="Show"
              value={show}
              onChange={(e) => setShow(e.target.value as Show)}
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

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="rounded-lg border bg-white overflow-hidden">
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
                {shown.map((u) => (
                  <tr
                    key={u.id}
                    className={`cursor-pointer hover:bg-blue-50 transition-colors ${u.disabled ? 'opacity-60' : ''}`}
                    onClick={() => navigate({ to: '/admin/users/$id', params: { id: u.id } })}
                  >
                    <td className="px-4 py-3">
                      <span className="font-medium text-gray-900">{u.display_name}</span>
                      {u.service_account && (
                        <Badge variant="secondary" className="ml-2 text-[10px]">Service</Badge>
                      )}
                      {u.disabled && (
                        <Badge variant="secondary" className="ml-2 text-[10px]">Disabled</Badge>
                      )}
                    </td>
                    <td className="px-4 py-3 text-gray-600">{u.email}</td>
                    <td className="px-4 py-3">
                      <Badge variant={roleBadge(u.role) as never}>{u.role}</Badge>
                    </td>
                    <td className="px-4 py-3 text-gray-500 text-xs">
                      {u.service_account
                        ? 'Never signs in'
                        : u.auth_type === 'saml' ? 'SSO' : u.auth_type === 'both' ? 'Local + SSO' : 'Local'}
                    </td>
                    <td className="px-4 py-3 text-gray-500">{new Date(u.created_at).toLocaleDateString()}</td>
                  </tr>
                ))}
                {shown.length === 0 && (
                  <tr><td colSpan={5} className="px-4 py-8 text-center text-gray-400">No users found.</td></tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Layout>
  )
}
