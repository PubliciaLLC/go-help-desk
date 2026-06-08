import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listAPIKeys, createAPIKey, deleteAPIKey } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon, CopyIcon, KeyIcon } from 'lucide-react'
import type { APIKey } from '@/api/types'

function TokenBanner({ token, onDismiss }: { token: string; onDismiss: () => void }) {
  const [copied, setCopied] = useState(false)

  function copy() {
    navigator.clipboard.writeText(token)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="rounded-lg border border-green-200 bg-green-50 p-4 space-y-3">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-sm font-medium text-green-800">API key created — copy it now</p>
          <p className="text-xs text-green-700 mt-0.5">This token will not be shown again.</p>
        </div>
        <button onClick={onDismiss} className="text-green-600 hover:text-green-800 text-xs">Dismiss</button>
      </div>
      <div className="flex items-center gap-2">
        <code className="flex-1 rounded border border-green-200 bg-white px-3 py-2 font-mono text-xs text-gray-800 break-all">
          {token}
        </code>
        <Button size="sm" variant="outline" onClick={copy} className="shrink-0">
          <CopyIcon className="h-3.5 w-3.5 mr-1" />
          {copied ? 'Copied!' : 'Copy'}
        </Button>
      </div>
    </div>
  )
}

export function APIKeysPage() {
  const qc = useQueryClient()
  const [newName, setNewName] = useState('')
  const [createError, setCreateError] = useState('')
  const [newToken, setNewToken] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<APIKey | null>(null)

  const { data: keys = [], isLoading } = useQuery({
    queryKey: ['admin', 'api-keys'],
    queryFn: listAPIKeys,
  })

  const createMutation = useMutation({
    mutationFn: () => createAPIKey({ name: newName.trim(), scopes: [] }),
    onSuccess: (res) => {
      setNewName('')
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
          <div className="flex items-center gap-3">
            <Input
              placeholder="Key name (e.g. Hermes, email-bridge)"
              value={newName}
              onChange={(e) => { setNewName(e.target.value); setCreateError('') }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && newName.trim()) createMutation.mutate()
              }}
              className="max-w-xs"
            />
            <Button
              onClick={() => createMutation.mutate()}
              disabled={!newName.trim() || createMutation.isPending}
            >
              <PlusIcon className="mr-2 h-4 w-4" />
              {createMutation.isPending ? 'Creating…' : 'Create'}
            </Button>
          </div>
          {createError && <p className="mt-2 text-sm text-red-600">{createError}</p>}
        </div>

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="rounded-lg border bg-white overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
                <tr>
                  <th className="px-4 py-3 text-left">Name</th>
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
                    <td colSpan={5} className="px-4 py-8 text-center text-gray-400">
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
        onOpenChange={(open) => { if (!open) setPendingDelete(null) }}
        title={`Revoke key "${pendingDelete?.name ?? ''}"?`}
        description="Any service using this key will immediately lose access. This cannot be undone."
        confirmLabel="Revoke"
        isPending={deleteMutation.isPending}
        onConfirm={() => { if (pendingDelete) deleteMutation.mutate(pendingDelete.id) }}
      />
    </Layout>
  )
}
