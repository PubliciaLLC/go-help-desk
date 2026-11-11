import { useState, useEffect } from 'react'
import { authTypeLabel } from '@/lib/authType'
import { useParams, useNavigate, Link } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  getUser, updateUser, adminResetPassword, deleteUser,
  listGroups, addGroupMember, removeGroupMember,
  listUserPasskeys, removeUserPasskey,
} from '@/api/admin'
import type { Passkey } from '@/api/passkeys'
import { useAuthStore } from '@/store/auth'
import { extractError, extractErrorCode } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { ArrowLeftIcon, ShieldCheckIcon, ShieldOffIcon, TrashIcon } from 'lucide-react'
import type { Role } from '@/api/types'

const ROLES: Role[] = ['admin', 'staff', 'user']
// A service user is never an administrator: the database refuses it (#420).
const SERVICE_ROLES: Role[] = ['staff', 'user']

function SectionCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0 rounded-lg border bg-white">
      <div className="border-b px-5 py-3">
        <h2 className="text-sm font-semibold text-gray-700">{title}</h2>
      </div>
      <div className="px-5 py-4 space-y-4">{children}</div>
    </div>
  )
}

// How a key is shown when its owner did not name it: its transports, never the
// authenticator model. The same wording as the owner's own Account page.
function keyLabel(p: Passkey): string {
  if (p.name) return p.name
  const t = p.transports?.length ? p.transports.join(', ') : 'unknown'
  return `Unnamed key (${t})`
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
  // Shared by the account actions below. Each of them could be refused with a
  // reason the server spells out — "this is the only administrator, so it
  // cannot be disabled, demoted or deleted" is the one that bites — and each
  // of them used to drop it: the button said "Disabling…", came back, and the
  // account was still enabled with nothing said. The role change beside them
  // showed its error all along, so the same refusal was explained on one
  // button and swallowed on the next.
  const [actionError, setActionError] = useState('')
  const [passwordSaved, setPasswordSaved] = useState(false)
  // Unticked every time: revoking someone's keys is a decision made for this
  // reset, not a preference that carries over to the next one (#450).
  const [revokeKeys, setRevokeKeys] = useState(false)

  // ── Group state ─────────────────────────────────────────────────────────────
  const [addGroupId, setAddGroupId] = useState('')
  const [groupError, setGroupError] = useState('')

  // ── Delete confirm ──────────────────────────────────────────────────────────
  const [confirmDelete, setConfirmDelete] = useState(false)

  // ── Passkey removal ─────────────────────────────────────────────────────────
  const [pendingKey, setPendingKey] = useState<Passkey | null>(null)
  const [keyError, setKeyError] = useState('')
  // The server refuses both removing your own key and resetting your own MFA
  // here (409 own_account): this route would go around the factor guard the
  // Account page holds. So the page does not offer them.
  const isOwnAccount = useAuthStore((s) => s.user?.id) === id

  const { data: user, isLoading } = useQuery({
    queryKey: ['admin', 'users', id],
    queryFn: () => getUser(id),
  })

  const passkeyCount = user?.passkey_count ?? 0
  const { data: passkeys = [] } = useQuery({
    queryKey: ['admin', 'users', id, 'passkeys'],
    queryFn: () => listUserPasskeys(id),
    enabled: passkeyCount > 0 && !isOwnAccount && !user?.service_account,
  })

  const { data: allGroups = [] } = useQuery({
    queryKey: ['admin', 'groups'],
    queryFn: listGroups,
  })

  /* The effects below seed local form state from a react-query result. That is
     the cascading-render pattern react-hooks/set-state-in-effect warns about
     (https://react.dev/learn/you-might-not-need-an-effect). Suppressed here,
     not refactored: switching the lint gate on should not also change how the
     admin forms behave. TODO: derive the state, or remount the form with a key.
     eslint-disable-next-line is not enough — the rule reports at each setState
     call, so the whole effect is wrapped. */
  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    if (user) {
      setDisplayName(user.display_name)
      setEmail(user.email)
      setRole(user.role)
    }
  }, [user])
  /* eslint-enable react-hooks/set-state-in-effect */

  function invalidate() {
    qc.invalidateQueries({ queryKey: ['admin', 'users', id] })
    qc.invalidateQueries({ queryKey: ['admin', 'users'] })
    // A group added to (or a disable of) a service user is what clears the
    // "OAuth clients see no tickets" banner; without this it stays up to five
    // minutes after the fix.
    qc.invalidateQueries({ queryKey: ['security-warnings'] })
  }

  // ── Profile mutation ────────────────────────────────────────────────────────
  const profileMutation = useMutation({
    // A service user's address is made by the system, so it is never sent back.
    mutationFn: () =>
      updateUser(id, user?.service_account
        ? { display_name: displayName, role }
        : { display_name: displayName, email, role }),
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
    onSuccess: () => { setActionError(''); invalidate() },
    onError: (err) => setActionError(extractError(err)),
  })

  // ── Reset MFA ───────────────────────────────────────────────────────────────
  const resetMFAMutation = useMutation({
    mutationFn: () => updateUser(id, { reset_mfa: true }),
    onSuccess: () => { setActionError(''); invalidate() },
    onError: (err) => setActionError(extractError(err)),
  })

  // ── Remove one passkey ──────────────────────────────────────────────────────
  const removeKeyMutation = useMutation({
    mutationFn: (credId: string) => removeUserPasskey(id, credId),
    onSuccess: () => {
      setKeyError('')
      qc.invalidateQueries({ queryKey: ['admin', 'users', id, 'passkeys'] })
      invalidate()
    },
    onError: (err) => setKeyError(extractError(err)),
  })

  // ── Password reset ──────────────────────────────────────────────────────────
  const passwordMutation = useMutation({
    mutationFn: () => adminResetPassword(id, newPassword, revokeKeys),
    onSuccess: () => {
      setNewPassword('')
      setRevokeKeys(false)
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
    onSuccess: () => { setGroupError(''); invalidate() },
    onError: (err) => setGroupError(extractError(err)),
  })

  // ── Delete ──────────────────────────────────────────────────────────────────
  const deleteMutation = useMutation({
    mutationFn: () => deleteUser(id),
    onSuccess: () => navigate({ to: '/admin/users' }),
    // Deleting the only administrator is refused. The dialog used to close
    // on its own and leave the account sitting there.
    onError: () => setConfirmDelete(false),
  })

  if (isLoading || !user) {
    return <Layout><div className="flex justify-center py-16"><Spinner /></div></Layout>
  }

  // mfa_enabled is the TOTP flag alone; a passkey is a second factor too.
  const isService = !!user.service_account
  const hasSecondFactor = !isService && (user.mfa_enabled || (user.passkey_count ?? 0) > 0)

  const memberGroupIds = new Set(user.groups.map((g) => g.id))
  const availableGroups = allGroups.filter((g) => !memberGroupIds.has(g.id))

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
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="min-w-0 text-2xl font-bold text-gray-900 wrap-anywhere">{user.display_name}</h1>
            {isService && <Badge variant="secondary" className="text-xs">Service</Badge>}
            {user.disabled && (
              <Badge variant="secondary" className="text-xs">Disabled</Badge>
            )}
          </div>
          {isService ? (
            <p className="mt-1 text-sm text-gray-500">
              The account an OAuth client acts as. It never signs in. Renaming it renames the
              client, its groups are the client&apos;s ticket scope, and disabling it stops the
              client. It is deleted by deleting the client on the{' '}
              <Link to="/admin/oauth-clients" className="text-blue-600 hover:underline">
                OAuth clients
              </Link>{' '}
              page.
            </p>
          ) : (
            <p className="mt-1 text-sm text-gray-500 wrap-anywhere">{user.email}</p>
          )}
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
            {!isService && (
              <FieldRow label="Email address">
                <Input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  disabled={user.disabled}
                />
              </FieldRow>
            )}
            <FieldRow label="Role">
              <Select
                value={role}
                onChange={(e) => setRole(e.target.value as Role)}
                disabled={user.disabled}
              >
                {(isService ? SERVICE_ROLES : ROLES).map((r) => (
                  <option key={r} value={r} className="capitalize">{r}</option>
                ))}
              </Select>
            </FieldRow>
            {user.disabled && (
              <p className="text-xs text-amber-600">Enable the account to edit profile fields.</p>
            )}
            {profileError && <p role="alert" className="text-sm text-red-600">{profileError}</p>}
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
                <p className="font-medium text-gray-800">{authTypeLabel(user.auth_type, true)}</p>
              </div>
              {!isService && (
              <div>
                <p className="text-xs text-gray-400 mb-0.5">MFA</p>
                <div className="flex items-center gap-1.5">
                  {hasSecondFactor ? (
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
              )}
              <div>
                <p className="text-xs text-gray-400 mb-0.5">Status</p>
                <p className={`font-medium ${user.disabled ? 'text-red-600' : 'text-green-700'}`}>
                  {user.disabled ? 'Disabled' : 'Active'}
                </p>
              </div>
            </div>

            <div className="border-t pt-3 space-y-3">
              {/* Passkeys: one at a time (#615) */}
              {!isService && !isOwnAccount && passkeys.length > 0 && (
                <div className="space-y-2">
                  <p className="text-sm font-medium text-gray-700">Passkeys</p>
                  <ul className="divide-y rounded-md border">
                    {passkeys.map((p) => (
                      <li key={p.id} className="flex items-center justify-between gap-2 px-4 py-2.5">
                        <div className="min-w-0">
                          <p className="truncate text-sm text-gray-800" title={keyLabel(p)}>{keyLabel(p)}</p>
                          <p className="text-xs text-gray-500">
                            {p.backup_eligible ? 'Synced' : 'This device only'}
                            {' · '}
                            added {new Date(p.created_at).toLocaleDateString()}
                            {' · '}
                            {p.last_used_at ? `last used ${new Date(p.last_used_at).toLocaleDateString()}` : 'never used'}
                          </p>
                        </div>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-red-600 hover:bg-red-50 hover:text-red-700"
                          onClick={() => setPendingKey(p)}
                          aria-label={`Remove ${keyLabel(p)}`}
                        >
                          <TrashIcon className="h-4 w-4" />
                        </Button>
                      </li>
                    ))}
                  </ul>
                  {keyError && <p role="alert" className="text-sm text-red-600">{keyError}</p>}
                </div>
              )}

              {isOwnAccount && hasSecondFactor && (
                <p className="text-xs text-gray-500">
                  Manage your own authenticator and passkeys on the Account page. Another administrator can reset yours.
                </p>
              )}

              {/* MFA reset */}
              {hasSecondFactor && !isOwnAccount && (
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm font-medium text-gray-700">Reset MFA</p>
                    <p className="text-xs text-gray-500">Clears the authenticator and every passkey, and signs them out everywhere — user re-enrols on next login.</p>
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

              {actionError && (
                <p role="alert" className="text-sm text-red-600">{actionError}</p>
              )}

              {/* Enable / Disable */}
              <div className="flex items-center justify-between">
                <div>
                  <p className="text-sm font-medium text-gray-700">
                    {user.disabled ? 'Enable account' : 'Disable account'}
                  </p>
                  <p className="text-xs text-gray-500">
                    {isService
                      ? user.disabled
                        ? 'Let the OAuth client work again.'
                        : 'Stops the OAuth client: its requests are refused until this is enabled again.'
                      : user.disabled
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
              {user.has_password && !isService && (
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
                  <label className="flex items-center gap-2 text-sm text-gray-600">
                    <input
                      type="checkbox"
                      className="h-4 w-4 rounded border-gray-300"
                      checked={revokeKeys}
                      onChange={(e) => setRevokeKeys(e.target.checked)}
                    />
                    Also revoke this person&apos;s API keys
                  </label>
                  {passwordError && <p role="alert" className="text-sm text-red-600">{passwordError}</p>}
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
                <div key={g.id} className="flex items-center justify-between gap-2 px-4 py-2.5">
                  <div className="min-w-0 wrap-anywhere">
                    <span className="text-sm font-medium text-gray-800">{g.name}</span>
                    {g.description && (
                      <span className="ml-2 text-xs text-gray-400">{g.description}</span>
                    )}
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="shrink-0 text-gray-400 hover:text-red-600"
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
                className="min-w-0 flex-1"
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
          {groupError && <p role="alert" className="text-sm text-red-600">{groupError}</p>}
        </SectionCard>

        {/* ── Danger zone ─────────────────────────────────────────────────── */}
        <div className="rounded-lg border border-red-200 bg-red-50 px-5 py-4">
          <h2 className="text-sm font-semibold text-red-700 mb-1">Danger zone</h2>
          <p className="text-sm text-red-600 mb-3">
            Permanently deletes this account. Tickets and replies they created are preserved but
            will show a removed user. This cannot be undone — disable the account instead if
            you may need to restore access.
          </p>
          {deleteMutation.isError && (
            <p role="alert" className="text-sm font-medium text-red-700 mb-3">
              {extractError(deleteMutation.error)}
              {extractErrorCode(deleteMutation.error) === 'service_account' && (
                <>
                  {' '}
                  <Link to="/admin/oauth-clients" className="underline">
                    Go to OAuth clients
                  </Link>
                </>
              )}
            </p>
          )}
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
      <ConfirmDialog
        open={pendingKey !== null}
        onOpenChange={(open) => !open && setPendingKey(null)}
        title="Remove this passkey?"
        description={
          pendingKey
            ? `"${keyLabel(pendingKey)}" will stop working, and ${user?.display_name ?? 'this person'} will be signed out everywhere. Their other keys and authenticator are not touched.`
            : undefined
        }
        confirmLabel="Remove"
        onConfirm={() => {
          if (pendingKey) removeKeyMutation.mutate(pendingKey.id)
          setPendingKey(null)
        }}
      />
    </Layout>
  )
}
