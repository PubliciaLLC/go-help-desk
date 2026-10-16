import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listAPIKeys, createAPIKey, deleteAPIKey, listScopes, listUsersPage } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { ScopePicker } from '@/components/admin/ScopePicker'
import { ExpiryField } from '@/components/admin/ExpiryField'
import { expiryToISO } from '@/lib/expiry'
import { TokenBanner } from '@/components/admin/TokenBanner'
import { PlusIcon, KeyIcon } from 'lucide-react'
import type { AdminUser, APIKey } from '@/api/types'

// Pick the account a key is issued for (#450). Searched rather than listed: a
// help desk can have thousands of requesters, and the user list is paged.
function OwnerPicker({
  value,
  onChange,
  disabled,
}: {
  value: AdminUser | null
  onChange: (u: AdminUser | null) => void
  disabled?: boolean
}) {
  const [text, setText] = useState('')
  const [q, setQ] = useState('')

  // Waits for a pause in typing so each keystroke is not a request.
  useEffect(() => {
    const t = setTimeout(() => setQ(text.trim()), 250)
    return () => clearTimeout(t)
  }, [text])

  const { data } = useQuery({
    queryKey: ['admin', 'users', 'issue-for', q],
    queryFn: () => listUsersPage(1, { q }),
    enabled: q.length >= 2 && value === null,
  })
  // A disabled account's keys are refused on every request, so it is not a
  // sensible owner, and the server refuses it.
  const matches = (data?.users ?? []).filter((u) => !u.disabled)

  if (value) {
    return (
      <div className="text-sm text-gray-700">
        <span className="font-medium">Issue for</span>{' '}
        {value.display_name} ({value.email}, {value.role}){' '}
        <button type="button" className="text-xs text-blue-600 hover:underline" onClick={() => { onChange(null); setText('') }}>
          Change
        </button>
      </div>
    )
  }
  return (
    <div className="space-y-1">
      <label className="block text-sm text-gray-700">
        <span className="font-medium">Issue for</span>
        <span className="ml-2 text-xs text-gray-500">optional — search by name or email; leave empty to make a key for yourself</span>
        <Input
          value={text}
          onChange={(e) => setText(e.target.value)}
          disabled={disabled}
          className="mt-1 max-w-xs"
        />
      </label>
      {matches.length > 0 && (
        <ul role="listbox" aria-label="Matching accounts" className="max-w-xs rounded border bg-white divide-y text-sm">
          {matches.map((u) => (
            <li key={u.id} role="presentation">
              <button type="button" role="option" aria-selected={false} className="w-full px-3 py-1.5 text-left hover:bg-gray-50" onClick={() => onChange(u)}>
                {u.display_name} <span className="text-xs text-gray-500">{u.email} · {u.role}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

export function APIKeysPage() {
  const qc = useQueryClient()
  const [newName, setNewName] = useState('')
  const [newScopes, setNewScopes] = useState<string[]>([])
  const [newExpires, setNewExpires] = useState('')
  const [issueFor, setIssueFor] = useState<AdminUser | null>(null)
  const [createError, setCreateError] = useState('')
  const [newToken, setNewToken] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<APIKey | null>(null)

  const { data: keys = [], isLoading } = useQuery({
    queryKey: ['admin', 'api-keys'],
    queryFn: listAPIKeys,
  })

  // Served by the API rather than hard-coded here, so the picker cannot offer a
  // scope the server does not enforce, or omit one it does.
  const { data: catalogue = [] } = useQuery({
    queryKey: ['admin', 'scopes'],
    queryFn: listScopes,
  })

  const createMutation = useMutation({
    mutationFn: () => createAPIKey({
      name: newName.trim(),
      scopes: newScopes,
      expires_at: expiryToISO(newExpires),
      user_id: issueFor?.id,
    }),
    onSuccess: (res) => {
      setNewName('')
      setNewScopes([])
      setNewExpires('')
      setIssueFor(null)
      setCreateError('')
      setNewToken(res.token)
      qc.invalidateQueries({ queryKey: ['admin', 'api-keys'] })
    },
    onError: (err) => setCreateError(extractError(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteAPIKey(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'api-keys'] })
      setPendingDelete(null)
    },
  })

  function fmt(date?: string) {
    if (!date) return '—'
    return new Date(date).toLocaleDateString()
  }

  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">API Keys</h1>
          <p className="mt-1 text-sm text-gray-500">
            API keys allow external services (Hermes, scripts, integrations) to authenticate with the REST API and MCP server.
          </p>
        </div>

        {newToken && (
          <TokenBanner token={newToken} onDismiss={() => setNewToken(null)} />
        )}

        <div className="rounded-lg border bg-white p-4">
          <p className="mb-3 text-sm font-medium text-gray-700">Create new key</p>
          <div className="space-y-4">
            <Input
              placeholder="Key name (e.g. Hermes, email-bridge)"
              value={newName}
              onChange={(e) => { setNewName(e.target.value); setCreateError('') }}
              className="max-w-xs"
            />

            <OwnerPicker value={issueFor} onChange={setIssueFor} disabled={createMutation.isPending} />

            <ScopePicker
              catalogue={catalogue}
              selected={newScopes}
              onChange={setNewScopes}
              disabled={createMutation.isPending}
            />

            <ExpiryField value={newExpires} onChange={setNewExpires} disabled={createMutation.isPending} />

            <Button
              onClick={() => createMutation.mutate()}
              disabled={!newName.trim() || newScopes.length === 0 || createMutation.isPending}
            >
              <PlusIcon className="mr-2 h-4 w-4" />
              {createMutation.isPending ? 'Creating…' : 'Create'}
            </Button>
          </div>
          {createError && <p role="alert" className="mt-2 text-sm text-red-600">{createError}</p>}
        </div>

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="rounded-lg border bg-white overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
                <tr>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Owner</th>
                  <th className="px-4 py-3 text-left">Permissions</th>
                  <th className="px-4 py-3 text-left">Created</th>
                  <th className="px-4 py-3 text-left">Last used</th>
                  <th className="px-4 py-3 text-left">Expires</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {keys.map((k) => (
                  <tr key={k.id} className="hover:bg-gray-50">
                    <td className="px-4 py-3 font-medium text-gray-900 flex items-center gap-2">
                      <KeyIcon className="h-3.5 w-3.5 text-gray-400 shrink-0" />
                      {k.name}
                    </td>
                    <td className="px-4 py-3">
                      <div className="text-gray-900">{k.owner_name}</div>
                      <div className="text-xs text-gray-500">{k.owner_email}</div>
                      {k.created_by && k.created_by !== k.user_id && (
                        <div className="text-xs text-gray-500">Issued on their behalf</div>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      {k.scopes?.length ? (
                        <div className="flex flex-wrap gap-1">
                          {k.scopes.map((sc) => (
                            <span key={sc} className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-700">
                              {sc}
                            </span>
                          ))}
                        </div>
                      ) : (
                        <span className="text-xs text-amber-700">
                          None — this key is refused everywhere
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-gray-500">{fmt(k.created_at)}</td>
                    <td className="px-4 py-3 text-gray-500">{fmt(k.last_used_at)}</td>
                    <td className="px-4 py-3 text-gray-500">{fmt(k.expires_at)}</td>
                    <td className="px-4 py-3 text-right">
                      <Button
                        size="sm"
                        variant="outline"
                        className="text-red-600 border-red-200 hover:bg-red-50"
                        onClick={() => setPendingDelete(k)}
                        disabled={deleteMutation.isPending}
                      >
                        Revoke
                      </Button>
                    </td>
                  </tr>
                ))}
                {keys.length === 0 && (
                  <tr>
                    <td colSpan={7} className="px-4 py-8 text-center text-gray-400">
                      No API keys yet.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => { if (!open) { setPendingDelete(null); deleteMutation.reset() } }}
        error={deleteMutation.isError ? extractError(deleteMutation.error) : undefined}
        title={`Revoke key "${pendingDelete?.name ?? ''}"?`}
        description="Any service using this key will immediately lose access. This cannot be undone."
        confirmLabel="Revoke"
        isPending={deleteMutation.isPending}
        onConfirm={() => { if (pendingDelete) deleteMutation.mutate(pendingDelete.id) }}
      />
    </Layout>
  )
}
