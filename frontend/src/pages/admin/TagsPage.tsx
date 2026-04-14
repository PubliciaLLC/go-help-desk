import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listAllTags, createTag, deleteTag, restoreTag } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon } from 'lucide-react'
import type { Tag } from '@/api/types'

export function TagsPage() {
  const qc = useQueryClient()
  const [newName, setNewName] = useState('')
  const [createError, setCreateError] = useState('')

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
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'tags'] }),
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
            <h1 className="text-2xl font-bold text-gray-900">Tags</h1>
            <p className="mt-1 text-sm text-gray-500">
              Tags are created by staff when tagging tickets, or added here directly. Admins can deactivate
              inappropriate tags or restore previously deactivated ones.
            </p>
          </div>
        </div>

        {/* Create tag form */}
        <div className="rounded-lg border bg-white p-4">
          <p className="mb-3 text-sm font-medium text-gray-700">Add tag</p>
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
                  <th className="px-4 py-3 text-left">Status</th>
                  <th className="px-4 py-3 text-left">Created</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {tags.map((t) => (
                  <tr key={t.id} className={isDeleted(t) ? 'opacity-50' : ''}>
                    <td className="px-4 py-3 font-medium text-gray-900">{t.name}</td>
                    <td className="px-4 py-3">
                      {isDeleted(t) ? (
                        <Badge variant="secondary">Deactivated</Badge>
                      ) : (
                        <Badge variant="default">Active</Badge>
                      )}
                    </td>
                    <td className="px-4 py-3 text-gray-500">
                      {new Date(t.created_at).toLocaleDateString()}
                    </td>
                    <td className="px-4 py-3 text-right">
                      {isDeleted(t) ? (
                        <Button
                          size="sm"
                          variant="outline"
                          className="text-green-700 border-green-300 hover:bg-green-50"
                          onClick={() => restoreMutation.mutate(t.id)}
                          disabled={restoreMutation.isPending}
                        >
                          Restore
                        </Button>
                      ) : (
                        <Button
                          size="sm"
                          variant="outline"
                          className="text-red-600 border-red-200 hover:bg-red-50"
                          onClick={() => deleteMutation.mutate(t.id)}
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
                    <td colSpan={4} className="px-4 py-8 text-center text-gray-400">
                      No tags yet.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Layout>
  )
}
