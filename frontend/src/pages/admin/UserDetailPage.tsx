import { useState, useEffect } from 'react'
import { useParams, useNavigate } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  getUser, updateUser, adminResetPassword, deleteUser,
  listGroups, addGroupMember, removeGroupMember,
} from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { ArrowLeftIcon, ShieldCheckIcon, ShieldOffIcon } from 'lucide-react'
import type { Role } from '@/api/types'

const ROLES: Role[] = ['admin', 'staff', 'user']

function SectionCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border bg-white">
      <div className="border-b px-5 py-3">
        <h2 className="text-sm font-semibold text-gray-700">{title}</h2>
      </div>
      <div className="px-5 py-4 space-y-4">{children}</div>
    </div>
  )
}

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <Label className="text-xs text-gray-500">{label}</Label>
      {children}
    </div>
  )
}

export function UserDetailPage() {
  const { id } = useParams({ from: '/admin/users/$id' })
  const navigate = useNavigate()
  const qc = useQueryClient()

  // ── Profile state ───────────────────────────────────────────────────────────
  const [displayName, setDisplayName] = useState('')
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('user')
  const [profileError, setProfileError] = useState('')
  const [profileSaved, setProfileSaved] = useState(false)

  // ── Password state ──────────────────────────────────────────────────────────
  const [newPassword, setNewPassword] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [passwordSaved, setPasswordSaved] = useState(false)

  // ── Group state ─────────────────────────────────────────────────────────────
  const [addGroupId, setAddGroupId] = useState('')
  const [groupError, setGroupError] = useState('')

  // ── Delete confirm ──────────────────────────────────────────────────────────
  const [confirmDelete, setConfirmDelete] = useState(false)

  const { data: user, isLoading } = useQuery({
    queryKey: ['admin', 'users', id],
    queryFn: () => getUser(id),
  })

  const { data: allGroups = [] } = useQuery({
    queryKey: ['admin', 'groups'],
    queryFn: listGroups,
  })

  useEffect(() => {
    if (user) {
      setDisplayName(user.display_name)
      setEmail(user.email)
      setRole(user.role)
    }
  }, [user])

  function invalidate() {
    qc.invalidateQueries({ queryKey: ['admin', 'users', id] })
    qc.invalidateQueries({ queryKey: ['admin', 'users'] })
  }

  // ── Profile mutation ────────────────────────────────────────────────────────
  const profileMutation = useMutation({
    mutationFn: () => updateUser(id, { display_name: displayName, email, role }),
    onSuccess: () => {
      setProfileSaved(true)
      setProfileError('')
      setTimeout(() => setProfileSaved(false), 2500)
      invalidate()
    },
    onError: (err) => setProfileError(extractError(err)),
  })

  // ── Toggle disabled ─────────────────────────────────────────────────────────
  const toggleDisabledMutation = useMutation({
    mutationFn: (disabled: boolean) => updateUser(id, { disabled }),
    onSuccess: () => invalidate(),
  })

  // ── Reset MFA ───────────────────────────────────────────────────────────────
  const resetMFAMutation = useMutation({
    mutationFn: () => updateUser(id, { reset_mfa: true }),
    onSuccess: () => invalidate(),
  })

  // ── Password reset ──────────────────────────────────────────────────────────
  const passwordMutation = useMutation({
    mutationFn: () => adminResetPassword(id, newPassword),
    onSuccess: () => {
      setNewPassword('')
      setPasswordSaved(true)
      setPasswordError('')
      setTimeout(() => setPasswordSaved(false), 2500)
    },
    onError: (err) => setPasswordError(extractError(err)),
  })

  // ── Group mutations ─────────────────────────────────────────────────────────
  const addToGroupMutation = useMutation({
    mutationFn: () => addGroupMember(addGroupId, id),
    onSuccess: () => {
      setAddGroupId('')
      setGroupError('')
      invalidate()
    },
    onError: (err) => setGroupError(extractError(err)),
  })

  const removeFromGroupMutation = useMutation({
    mutationFn: (groupId: string) => removeGroupMember(groupId, id),
    onSuccess: () => invalidate(),
  })

  // ── Delete ──────────────────────────────────────────────────────────────────
  const deleteMutation = useMutation({
    mutationFn: () => deleteUser(id),
    onSuccess: () => navigate({ to: '/admin/users' }),
  })

  if (isLoading || !user) {
    return <Layout><div className="flex justify-center py-16"><Spinner /></div></Layout>
  }

  const memberGroupIds = new Set(user.groups.map((g) => g.id))
  const availableGroups = allGroups.filter((g) => !memberGroupIds.has(g.id))

  function authTypeLabel(t: string) {
    if (t === 'saml') return 'SSO (SAML)'
    if (t === 'both') return 'Local + SSO'
    return 'Local'
  }

  return (
    <Layout>
      <div className="space-y-6">
        {/* Header */}
        <div>
          <button
            onClick={() => navigate({ to: '/admin/users' })}
            className="mb-3 flex items-center gap-1.5 text-sm text-gray-500 hover:text-gray-800"
          >
            <ArrowLeftIcon className="h-3.5 w-3.5" />
            Back to Users
          </button>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold text-gray-900">{user.display_name}</h1>
            {user.disabled && (
              <Badge variant="secondary" className="text-xs">Disabled</Badge>
            )}
          </div>
          <p className="mt-1 text-sm text-gray-500">{user.email}</p>
        </div>

        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          {/* ── Profile ──────────────────────────────────────────────────── */}
          <SectionCard title="Profile">
            <FieldRow label="Display name">
              <Input
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                disabled={user.disabled}
              />
            </FieldRow>
            <FieldRow label="Email address">
              <Input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                disabled={user.disabled}
              />
            </FieldRow>
            <FieldRow label="Role">
              <Select
                value={role}
                onChange={(e) => setRole(e.target.value as Role)}
                disabled={user.disabled}
              >
                {ROLES.map((r) => (
                  <option key={r} value={r} className="capitalize">{r}</option>
                ))}
              </Select>
            </FieldRow>
            {user.disabled && (
              <p className="text-xs text-amber-600">Enable the account to edit profile fields.</p>
            )}
            {profileError && <p className="text-sm text-red-600">{profileError}</p>}
            <div className="flex items-center gap-3 pt-1">
              <Button
                size="sm"
                onClick={() => profileMutation.mutate()}
                disabled={profileMutation.isPending || user.disabled}
              >
                {profileMutation.isPending ? 'Saving…' : 'Save profile'}
              </Button>
              {profileSaved && <span className="text-sm text-green-600">Saved.</span>}
            </div>
          </SectionCard>

          {/* ── Account ──────────────────────────────────────────────────── */}
          <SectionCard title="Account">
            {/* Info grid */}
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div>
                <p className="text-xs text-gray-400 mb-0.5">Member since</p>
                <p className="font-medium text-gray-800">
                  {new Date(user.created_at).toLocaleDateString(undefined, { dateStyle: 'medium' })}
                </p>
              </div>
              <div>
                <p className="text-xs text-gray-400 mb-0.5">Login type</p>
                <p className="font-medium text-gray-800">{authTypeLabel(user.auth_type)}</p>
              </div>
              <div>
                <p className="text-xs text-gray-400 mb-0.5">MFA</p>
                <div className="flex items-center gap-1.5">
                  {user.mfa_enabled ? (
                    <>
                      <ShieldCheckIcon className="h-4 w-4 text-green-600" />
                      <span className="font-medium text-green-700">Enrolled</span>
                    </>
                  ) : (
                    <>
                      <ShieldOffIcon className="h-4 w-4 text-gray-400" />
                      <span className="font-medium text-gray-500">Not enrolled</span>
                    </>
                  )}
                </div>
              </div>
              <div>
                <p className="text-xs text-gray-400 mb-0.5">Status</p>
                <p className={`font-medium ${user.disabled ? 'text-red-600' : 'text-green-700'}`}>
                  {user.disabled ? 'Disabled' : 'Active'}
                </p>
              </div>
            </div>

            <div className="border-t pt-3 space-y-3">
              {/* MFA reset */}
              {user.mfa_enabled && (
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-gray-700">Reset MFA</p>
                    <p className="text-xs text-gray-500">Clears the TOTP secret — user re-enrolls on next login.</p>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => resetMFAMutation.mutate()}
                    disabled={resetMFAMutation.isPending}
                  >
                    {resetMFAMutation.isPending ? 'Resetting…' : 'Reset MFA'}
                  </Button>
                </div>
              )}

              {/* Enable / Disable */}
              <div className="flex items-center justify-between">
                <div>
                  <p className="text-sm font-medium text-gray-700">
                    {user.disabled ? 'Enable account' : 'Disable account'}
                  </p>
                  <p className="text-xs text-gray-500">
                    {user.disabled
                      ? 'Allow this user to sign in again.'
                      : 'Prevent this user from signing in. Tickets and history are preserved.'}
                  </p>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  className={user.disabled ? 'text-green-700 border-green-300 hover:bg-green-50' : 'text-red-600 border-red-200 hover:bg-red-50'}
                  onClick={() => toggleDisabledMutation.mutate(!user.disabled)}
                  disabled={toggleDisabledMutation.isPending}
                >
                  {toggleDisabledMutation.isPending
                    ? '…'
                    : user.disabled ? 'Enable' : 'Disable'}
                </Button>
              </div>

              {/* Password reset */}
              {user.has_password && (
                <div className="space-y-2 border-t pt-3">
                  <p className="text-sm font-medium text-gray-700">Reset password</p>
                  <div className="flex gap-2">
                    <Input
                      type="password"
                      placeholder="New password"
                      value={newPassword}
                      onChange={(e) => setNewPassword(e.target.value)}
                      className="flex-1"
                    />
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => passwordMutation.mutate()}
                      disabled={passwordMutation.isPending || !newPassword}
                    >
                      {passwordMutation.isPending ? 'Setting…' : 'Set password'}
                    </Button>
                  </div>
                  {passwordError && <p className="text-sm text-red-600">{passwordError}</p>}
                  {passwordSaved && <p className="text-sm text-green-600">Password updated.</p>}
                </div>
              )}
            </div>
          </SectionCard>
        </div>

        {/* ── Groups ─────────────────────────────────────────────────────── */}
        <SectionCard title="Groups">
          {user.groups.length === 0 ? (
            <p className="text-sm text-gray-400">Not a member of any groups.</p>
          ) : (
            <div className="divide-y rounded-md border">
              {user.groups.map((g) => (
                <div key={g.id} className="flex items-center justify-between px-4 py-2.5">
                  <div>
                    <span className="text-sm font-medium text-gray-800">{g.name}</span>
                    {g.description && (
                      <span className="ml-2 text-xs text-gray-400">{g.description}</span>
                    )}
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-gray-400 hover:text-red-600"
                    onClick={() => removeFromGroupMutation.mutate(g.id)}
                    disabled={removeFromGroupMutation.isPending}
                  >
                    Remove
                  </Button>
                </div>
              ))}
            </div>
          )}

          {availableGroups.length > 0 && (
            <div className="flex gap-2 pt-1">
              <Select
                value={addGroupId}
                onChange={(e) => setAddGroupId(e.target.value)}
                className="flex-1"
              >
                <option value="">Add to group…</option>
                {availableGroups.map((g) => (
                  <option key={g.id} value={g.id}>{g.name}</option>
                ))}
              </Select>
              <Button
                size="sm"
                onClick={() => addToGroupMutation.mutate()}
                disabled={!addGroupId || addToGroupMutation.isPending}
              >
                {addToGroupMutation.isPending ? 'Adding…' : 'Add'}
              </Button>
            </div>
          )}
          {groupError && <p className="text-sm text-red-600">{groupError}</p>}
        </SectionCard>

        {/* ── Danger zone ─────────────────────────────────────────────────── */}
        <div className="rounded-lg border border-red-200 bg-red-50 px-5 py-4">
          <h2 className="text-sm font-semibold text-red-700 mb-1">Danger zone</h2>
          <p className="text-sm text-red-600 mb-3">
            Permanently deletes this account. Tickets and replies they created are preserved but
            will show a removed user. This cannot be undone — disable the account instead if
            you may need to restore access.
          </p>
          <Button
            size="sm"
            variant="outline"
            className="border-red-300 text-red-600 hover:bg-red-100"
            onClick={() => setConfirmDelete(true)}
          >
            Delete user
          </Button>
        </div>
      </div>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Permanently delete "${user?.display_name ?? 'user'}"?`}
        description="Their tickets and replies are preserved but show as a removed user. This cannot be undone."
        confirmLabel="Delete user"
        isPending={deleteMutation.isPending}
        onConfirm={() => deleteMutation.mutate()}
      />
    </Layout>
  )
}
