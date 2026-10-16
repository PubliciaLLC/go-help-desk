import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyIcon, PlusIcon } from 'lucide-react'
import { createMyAPIKey, deleteMyAPIKey, listMyAPIKeys, listMyAPIKeyScopes } from '@/api/apikeys'
import { extractError } from '@/api/client'
import { ScopePicker } from '@/components/admin/ScopePicker'
import { ExpiryField } from '@/components/admin/ExpiryField'
import { expiryToISO } from '@/lib/expiry'
import { TokenBanner } from '@/components/admin/TokenBanner'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import type { MyAPIKey } from '@/api/types'
import { useAuthStore } from '@/store/auth'

// The caller's own API keys (#450).
//
// Shown to administrators, and to staff who may create keys or already hold
// one (an administrator can issue one for them, and they must be able to see
// and revoke it whatever the setting says). A requester never sees it and the
// page does not ask the server about it.

function fmt(date?: string) {
  return date ? new Date(date).toLocaleDateString() : '—'
}

function Form({ onCreated }: { onCreated: (token: string) => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState<string[]>([])
  const [expires, setExpires] = useState('')
  const [error, setError] = useState('')

  // Served by the API for this role, so the picker offers what will be accepted.
  const { data: catalogue = [] } = useQuery({ queryKey: ['me', 'api-key-scopes'], queryFn: listMyAPIKeyScopes })

  const create = useMutation({
    mutationFn: () => createMyAPIKey({ name: name.trim(), scopes, expires_at: expiryToISO(expires) }),
    onSuccess: (res) => {
      setName('')
      setScopes([])
      setExpires('')
      setError('')
      onCreated(res.token)
      qc.invalidateQueries({ queryKey: ['me', 'api-keys'] })
    },
    onError: (err) => setError(extractError(err)),
  })

  return (
    <div className="space-y-4">
      <Input
        placeholder="Key name (e.g. my script)"
        value={name}
        onChange={(e) => { setName(e.target.value); setError('') }}
        className="max-w-xs"
      />
      <ScopePicker catalogue={catalogue} selected={scopes} onChange={setScopes} disabled={create.isPending} />
      <ExpiryField value={expires} onChange={setExpires} disabled={create.isPending} />
      <Button onClick={() => create.mutate()} disabled={!name.trim() || scopes.length === 0 || create.isPending}>
        <PlusIcon className="mr-2 h-4 w-4" />
        {create.isPending ? 'Creating…' : 'Create key'}
      </Button>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
    </div>
  )
}

export function ApiKeysSection() {
  const user = useAuthStore((s) => s.user)
  const qc = useQueryClient()
  const [token, setToken] = useState<string | null>(null)
  const [pending, setPending] = useState<MyAPIKey | null>(null)

  const eligible = user?.role === 'admin' || user?.role === 'staff'
  const { data: keys = [], isSuccess } = useQuery({
    queryKey: ['me', 'api-keys'],
    queryFn: listMyAPIKeys,
    enabled: eligible,
  })

  const revoke = useMutation({
    mutationFn: (id: string) => deleteMyAPIKey(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['me', 'api-keys'] })
      setPending(null)
    },
  })

  const canCreate = user?.can_create_api_keys === true
  if (!eligible || !isSuccess || (!canCreate && keys.length === 0)) return null

  return (
    <section className="rounded-lg border bg-white">
      <div className="flex items-start gap-3 border-b p-4">
        <div className="mt-0.5 text-gray-400"><KeyIcon className="h-4 w-4" /></div>
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-gray-900">API keys</h2>
          <p className="mt-0.5 text-xs text-gray-500">
            For your own scripts and tools. A key acts as you, with at most the permissions you choose here.
          </p>
        </div>
      </div>
      <div className="space-y-4 p-4">
        {token && <TokenBanner token={token} onDismiss={() => setToken(null)} />}

        {canCreate ? (
          <Form onCreated={setToken} />
        ) : (
          <p className="text-sm text-gray-600">
            An administrator has not allowed staff to create their own API keys. You can still revoke the ones below.
          </p>
        )}

        {keys.length > 0 && (
          <table className="w-full text-sm">
            <thead className="text-xs uppercase text-gray-500">
              <tr>
                <th className="py-2 text-left">Name</th>
                <th className="py-2 text-left">Permissions</th>
                <th className="py-2 text-left">Last used</th>
                <th className="py-2 text-left">Expires</th>
                <th className="py-2 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {keys.map((k) => (
                <tr key={k.id}>
                  <td className="py-2 font-medium text-gray-900">
                    {k.name}
                    {k.created_by && k.created_by !== k.user_id && (
                      <div className="text-xs font-normal text-gray-500">Issued for you by an administrator</div>
                    )}
                  </td>
                  <td className="py-2">
                    <div className="flex flex-wrap gap-1">
                      {k.scopes.map((sc) => (
                        <span key={sc} className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-700">{sc}</span>
                      ))}
                    </div>
                  </td>
                  <td className="py-2 text-gray-500">{fmt(k.last_used_at)}</td>
                  <td className="py-2 text-gray-500">{fmt(k.expires_at)}</td>
                  <td className="py-2 text-right">
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-red-600 border-red-200 hover:bg-red-50"
                      onClick={() => setPending(k)}
                    >
                      Revoke
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => { if (!open) { setPending(null); revoke.reset() } }}
        error={revoke.isError ? extractError(revoke.error) : undefined}
        title={`Revoke key "${pending?.name ?? ''}"?`}
        description="Anything using this key will immediately lose access. This cannot be undone."
        confirmLabel="Revoke"
        isPending={revoke.isPending}
        onConfirm={() => { if (pending) revoke.mutate(pending.id) }}
      />
    </section>
  )
}
