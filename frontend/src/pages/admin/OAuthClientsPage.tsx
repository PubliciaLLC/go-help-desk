import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listOAuthClients, createOAuthClient, deleteOAuthClient, listScopes } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { ScopePicker } from '@/components/admin/ScopePicker'
import { PlusIcon, CopyIcon, PlugIcon } from 'lucide-react'
import type { OAuthClient } from '@/api/types'

function SecretBanner({
  clientID,
  secret,
  onDismiss,
}: {
  clientID: string
  secret: string
  onDismiss: () => void
}) {
  const [copied, setCopied] = useState(false)

  function copy() {
    navigator.clipboard.writeText(secret)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="rounded-lg border border-green-200 bg-green-50 p-4 space-y-3">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-sm font-medium text-green-800">
            Client created — copy the secret now
          </p>
          <p className="text-xs text-green-700 mt-0.5">It will not be shown again.</p>
        </div>
        <button onClick={onDismiss} className="text-green-600 hover:text-green-800 text-xs">
          Dismiss
        </button>
      </div>
      <div className="space-y-2">
        <div>
          <p className="text-xs text-green-700 mb-1">Client ID</p>
          <code className="block rounded border border-green-200 bg-white px-3 py-2 font-mono text-xs text-gray-800 break-all">
            {clientID}
          </code>
        </div>
        <div>
          <p className="text-xs text-green-700 mb-1">Client secret</p>
          <div className="flex items-center gap-2">
            <code className="flex-1 rounded border border-green-200 bg-white px-3 py-2 font-mono text-xs text-gray-800 break-all">
              {secret}
            </code>
            <Button size="sm" variant="outline" onClick={copy} className="shrink-0">
              <CopyIcon className="h-3.5 w-3.5 mr-1" />
              {copied ? 'Copied!' : 'Copy'}
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}

export function OAuthClientsPage() {
  const qc = useQueryClient()
  const [newName, setNewName] = useState('')
  const [newScopes, setNewScopes] = useState<string[]>([])
  const [createError, setCreateError] = useState('')
  const [created, setCreated] = useState<{ clientID: string; secret: string } | null>(null)
  const [pendingDelete, setPendingDelete] = useState<OAuthClient | null>(null)

  const { data: clients = [], isLoading } = useQuery({
    queryKey: ['admin', 'oauth-clients'],
    queryFn: listOAuthClients,
  })

  const { data: catalogue = [] } = useQuery({
    queryKey: ['admin', 'scopes'],
    queryFn: listScopes,
  })

  const createMutation = useMutation({
    mutationFn: () => createOAuthClient({ name: newName.trim(), scopes: newScopes }),
    onSuccess: (res) => {
      setNewName('')
      setNewScopes([])
      setCreateError('')
      setCreated({ clientID: res.client_id, secret: res.client_secret })
      qc.invalidateQueries({ queryKey: ['admin', 'oauth-clients'] })
    },
    onError: (err) => setCreateError(extractError(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteOAuthClient(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'oauth-clients'] })
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
          <h1 className="text-2xl font-bold text-gray-900">OAuth Clients</h1>
          <p className="mt-1 text-sm text-gray-500">
            Machine-to-machine integrations exchange a client ID and secret for a short-lived
            token. Deleting a client stops its existing tokens working immediately.
          </p>
        </div>

        {created && (
          <SecretBanner
            clientID={created.clientID}
            secret={created.secret}
            onDismiss={() => setCreated(null)}
          />
        )}

        <div className="rounded-lg border bg-white p-4">
          <p className="mb-3 text-sm font-medium text-gray-700">Create new client</p>
          <div className="space-y-4">
            <Input
              placeholder="Client name (e.g. JIRA sync, CI pipeline)"
              value={newName}
              onChange={(e) => {
                setNewName(e.target.value)
                setCreateError('')
              }}
              className="max-w-xs"
            />

            <ScopePicker
              catalogue={catalogue}
              selected={newScopes}
              onChange={setNewScopes}
              disabled={createMutation.isPending}
            />

            <Button
              onClick={() => createMutation.mutate()}
              disabled={!newName.trim() || newScopes.length === 0 || createMutation.isPending}
            >
              <PlusIcon className="mr-2 h-4 w-4" />
              {createMutation.isPending ? 'Creating…' : 'Create'}
            </Button>
          </div>
          {createError && <p className="mt-2 text-sm text-red-600">{createError}</p>}
        </div>

        {isLoading ? (
          <div className="flex justify-center py-12">
            <Spinner />
          </div>
        ) : (
          <div className="rounded-lg border bg-white overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
                <tr>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Client ID</th>
                  <th className="px-4 py-3 text-left">Permissions</th>
                  <th className="px-4 py-3 text-left">Created</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {clients.map((c) => (
                  <tr key={c.id} className="hover:bg-gray-50">
                    <td className="px-4 py-3 font-medium text-gray-900">
                      <span className="flex items-center gap-2">
                        <PlugIcon className="h-3.5 w-3.5 text-gray-400 shrink-0" />
                        {c.name}
                      </span>
                    </td>
                    <td className="px-4 py-3 font-mono text-xs text-gray-600">{c.client_id}</td>
                    <td className="px-4 py-3">
                      {c.scopes?.length ? (
                        <div className="flex flex-wrap gap-1">
                          {c.scopes.map((sc) => (
                            <span
                              key={sc}
                              className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-700"
                            >
                              {sc}
                            </span>
                          ))}
                        </div>
                      ) : (
                        <span className="text-xs text-amber-700">
                          None — this client is refused everywhere
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-gray-500">{fmt(c.created_at)}</td>
                    <td className="px-4 py-3 text-right">
                      <Button
                        size="sm"
                        variant="outline"
                        className="text-red-600 border-red-200 hover:bg-red-50"
                        onClick={() => setPendingDelete(c)}
                        disabled={deleteMutation.isPending}
                      >
                        Revoke
                      </Button>
                    </td>
                  </tr>
                ))}
                {clients.length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-4 py-8 text-center text-gray-400">
                      No OAuth clients yet.
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
        onOpenChange={(open) => { if (!open) setPendingDelete(null) }}
        title={`Revoke client "${pendingDelete?.name ?? ''}"?`}
        description="Any integration using this client loses access immediately, including tokens it already holds. This cannot be undone."
        confirmLabel="Revoke"
        isPending={deleteMutation.isPending}
        onConfirm={() => { if (pendingDelete) deleteMutation.mutate(pendingDelete.id) }}
      />
    </Layout>
  )
}
