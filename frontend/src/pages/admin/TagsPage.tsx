import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listAllTags, deleteTag, restoreTag } from '@/api/admin'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import type { Tag } from '@/api/types'

export function TagsPage() {
  const qc = useQueryClient()

  const { data: tags = [], isLoading } = useQuery({
    queryKey: ['admin', 'tags'],
    queryFn: listAllTags,
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
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Tags</h1>
          <p className="mt-1 text-sm text-gray-500">
            Tags are created by staff when tagging tickets. Admins can deactivate inappropriate tags
            or restore previously deactivated ones.
          </p>
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
                      No tags yet. Tags are created when staff tag tickets.
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
