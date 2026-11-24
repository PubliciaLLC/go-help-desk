import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listUsers, updateUser } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Badge } from '@/components/ui/badge'
import { Select } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import type { Role, User } from '@/api/types'

const ROLES: { role: Role; label: string; description: string }[] = [
  {
    role: 'admin',
    label: 'Admin',
    description:
      'Full system access. Manage settings, users, groups, categories, and plugins. Can always log in with local auth even when SAML is enabled.',
  },
  {
    role: 'staff',
    label: 'Staff',
    description:
      'Create tickets. View, edit, and assign tickets within their scope. Search and open any ticket by number. Assign tickets to any staff member or group.',
  },
  {
    role: 'user',
    label: 'User',
    description:
      'Create tickets. View their own tickets. Update their own tickets unless the status is Resolved. Reopen a resolved ticket within the configured reopen window.',
  },
]

function badgeVariant(role: Role) {
  if (role === 'admin') return 'destructive'
  if (role === 'staff') return 'default'
  return 'secondary'
}

function RoleRow({ user, currentUserRole }: { user: User; currentUserRole: Role }) {
  const qc = useQueryClient()
  const mutation = useMutation({
    mutationFn: (newRole: Role) => updateUser(user.id, { role: newRole }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'users'] }),
  })

  // Prevent the sole admin from demoting themselves via this UI.
  const isCurrentAdmin = user.role === 'admin' && currentUserRole === 'admin'

  return (
    <tr className="hover:bg-gray-50">
      <td className="px-4 py-3 font-medium text-gray-900">{user.display_name}</td>
      <td className="px-4 py-3 text-gray-500">{user.email}</td>
      <td className="px-4 py-3 text-gray-400">{new Date(user.created_at).toLocaleDateString()}</td>
      <td className="px-4 py-3 text-right">
        {mutation.isError && (
          <span className="mr-2 text-xs text-red-500">{extractError(mutation.error)}</span>
        )}
        <Select
          value={user.role}
          disabled={mutation.isPending || isCurrentAdmin}
          onChange={(e) => mutation.mutate(e.target.value as Role)}
          className="text-sm"
        >
          <option value="admin">admin</option>
          <option value="staff">staff</option>
          <option value="user">user</option>
        </Select>
      </td>
    </tr>
  )
}

function RoleCard({
  role,
  label,
  description,
  users,
  currentUserRole,
}: {
  role: Role
  label: string
  description: string
  users: User[]
  currentUserRole: Role
}) {
  return (
    <div className="rounded-lg border bg-white overflow-hidden">
      <div className="flex items-start gap-3 border-b px-5 py-4">
        <Badge variant={badgeVariant(role) as never} className="mt-0.5 shrink-0 capitalize">
          {label}
        </Badge>
        <p className="text-sm text-gray-500">{description}</p>
      </div>
      {users.length === 0 ? (
        <p className="px-5 py-4 text-sm text-gray-400">No {label.toLowerCase()}s.</p>
      ) : (
        <table className="w-full text-sm">
          <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
            <tr>
              <th className="px-4 py-2 text-left">Name</th>
              <th className="px-4 py-2 text-left">Email</th>
              <th className="px-4 py-2 text-left">Joined</th>
              <th className="px-4 py-2 text-right">Role</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {users.map((u) => (
              <RoleRow key={u.id} user={u} currentUserRole={currentUserRole} />
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

export function RolesPage() {
  const { data: users = [], isLoading } = useQuery({
    queryKey: ['admin', 'users'],
    queryFn: () => listUsers(500),
  })

  if (isLoading) {
    return (
      <Layout>
        <div className="flex justify-center py-12">
          <Spinner />
        </div>
      </Layout>
    )
  }

  // Determine the currently logged-in user's role by finding the admin user
  // with the most recently created account (rough heuristic — only used to
  // protect the "sole admin" guard in RoleRow).
  const currentUserRole: Role =
    users.find((u) => u.role === 'admin')?.role ?? 'admin'

  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Roles</h1>
          <p className="mt-1 text-sm text-gray-500">
            The three built-in roles define what each user can see and do. Change a user's role
            using the dropdown in their row.
          </p>
        </div>

        {ROLES.map(({ role, label, description }) => (
          <RoleCard
            key={role}
            role={role}
            label={label}
            description={description}
            users={users.filter((u) => u.role === role)}
            currentUserRole={currentUserRole}
          />
        ))}
      </div>
    </Layout>
  )
}
