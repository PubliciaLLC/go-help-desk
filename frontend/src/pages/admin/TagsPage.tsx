import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listAllTags, createTag, deleteTag, restoreTag } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon } from 'lucide-react'
import type { Tag } from '@/api/types'

export function TagsPage() {
  const qc = useQueryClient()
  const [newName, setNewName] = useState('')
  const [createError, setCreateError] = useState('')
  const [pendingDelete, setPendingDelete] = useState<Tag | null>(null)

  const { data: tags = [], isLoading } = useQuery({
    queryKey: ['admin', 'tags'],
    queryFn: listAllTags,
  })

  const createMutation = useMutation({
    mutationFn: () => createTag(newName.trim()),
    onSuccess: () => {
      setNewName('')
      setCreateError('')
      qc.invalidateQueries({ queryKey: ['admin', 'tags'] })
    },
    onError: (err) => setCreateError(extractError(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteTag(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'tags'] })
      setPendingDelete(null)
    },
  })

  const restoreMutation = useMutation({
    mutationFn: (id: string) => restoreTag(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'tags'] }),
  })

  function isDeleted(t: Tag) {
    return !!t.deleted_at
  }

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-2xl font-bold text-fg">Tags</h1>
            <p className="mt-1 text-sm text-fg-faint">
              Tags are created by staff when tagging tickets, or added here directly. Admins can deactivate
              inappropriate tags or restore previously deactivated ones.
            </p>
          </div>
        </div>

        {/* Create tag form */}
        <div className="rounded-lg border bg-card p-4">
          <p className="mb-3 text-sm font-medium text-fg-soft">Add tag</p>
          <div className="flex items-center gap-3">
            <Input
              placeholder="Tag name (stored lowercase)"
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
              {createMutation.isPending ? 'Adding…' : 'Add'}
            </Button>
          </div>
          {createError && <p role="alert" className="mt-2 text-sm text-danger-fg">{createError}</p>}
        </div>

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="rounded-lg border bg-card overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-subtle text-xs text-fg-faint uppercase">
                <tr>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Status</th>
                  <th className="px-4 py-3 text-left">Created</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {tags.map((t) => (
                  <tr key={t.id} className={isDeleted(t) ? 'opacity-50' : ''}>
                    <td className="px-4 py-3 font-medium text-fg wrap-anywhere">{t.name}</td>
                    <td className="px-4 py-3">
                      {isDeleted(t) ? (
                        <Badge variant="secondary">Deactivated</Badge>
                      ) : (
                        <Badge variant="default">Active</Badge>
                      )}
                    </td>
                    <td className="px-4 py-3 text-fg-faint">
                      {new Date(t.created_at).toLocaleDateString()}
                    </td>
                    <td className="px-4 py-3 text-right">
                      {isDeleted(t) ? (
                        <Button
                          size="sm"
                          variant="outline"
                          className="text-success-fg border-success-border hover:bg-success-soft hover:text-success-fg-strong"
                          onClick={() => restoreMutation.mutate(t.id)}
                          disabled={restoreMutation.isPending}
                        >
                          Restore
                        </Button>
                      ) : (
                        <Button
                          size="sm"
                          variant="outline"
                          className="text-danger-fg border-danger-border hover:bg-danger-soft hover:text-danger-fg-strong"
                          onClick={() => setPendingDelete(t)}
                          disabled={deleteMutation.isPending}
                        >
                          Deactivate
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
                {tags.length === 0 && (
                  <tr>
                    <td colSpan={4} className="px-4 py-8 text-center text-fg-faint">
                      No tags yet.
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
        title={`Deactivate tag "${pendingDelete?.name ?? ''}"?`}
        description="The tag stays on tickets that already use it but can't be added to new tickets. You can restore it later."
        confirmLabel="Deactivate"
        isPending={deleteMutation.isPending}
        onConfirm={() => { if (pendingDelete) deleteMutation.mutate(pendingDelete.id) }}
      />
    </Layout>
  )
}
