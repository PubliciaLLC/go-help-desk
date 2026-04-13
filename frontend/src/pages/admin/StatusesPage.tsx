import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listStatuses, createStatus, deleteStatus } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon, TrashIcon, LockIcon } from 'lucide-react'

const DEFAULT_COLOR = '#6b7280'

export function StatusesPage() {
  const qc = useQueryClient()
  const [addingStatus, setAddingStatus] = useState(false)
  const [name, setName] = useState('')
  const [color, setColor] = useState(DEFAULT_COLOR)
  const [sortOrder, setSortOrder] = useState(10)
  const [formError, setFormError] = useState('')

  const { data: statuses = [], isLoading } = useQuery({
    queryKey: ['admin', 'statuses'],
    queryFn: listStatuses,
  })

  const createMutation = useMutation({
    mutationFn: () => createStatus({ name: name.trim(), color, sort_order: sortOrder }),
    onSuccess: () => {
      setName('')
      setColor(DEFAULT_COLOR)
      setSortOrder(10)
      setAddingStatus(false)
      setFormError('')
      qc.invalidateQueries({ queryKey: ['admin', 'statuses'] })
    },
    onError: (err) => setFormError(extractError(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteStatus(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'statuses'] }),
  })

  const sorted = [...statuses].sort((a, b) => a.sort_order - b.sort_order)

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900">Ticket Statuses</h1>
            <p className="mt-1 text-sm text-gray-500">
              Add custom intermediate statuses for your workflow. The three system statuses — New, Resolved, and Closed — have fixed lifecycle rules and cannot be removed.
            </p>
          </div>
          <Button onClick={() => setAddingStatus(true)} className="ml-6 shrink-0">
            <PlusIcon className="mr-2 h-4 w-4" />
            New Status
          </Button>
        </div>

        {addingStatus && (
          <div className="rounded-lg border bg-white p-4">
            <p className="mb-3 text-sm font-medium text-gray-700">New status</p>
            <div className="flex items-end gap-3">
              <div className="flex-1 space-y-1">
                <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">Name</label>
                <Input
                  autoFocus
                  placeholder="e.g. In Progress"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && name.trim()) createMutation.mutate()
                    if (e.key === 'Escape') { setAddingStatus(false); setName('') }
                  }}
                />
              </div>
              <div className="space-y-1">
                <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">Color</label>
                <div className="flex items-center gap-2 h-9">
                  <input
                    type="color"
                    value={color}
                    onChange={(e) => setColor(e.target.value)}
                    className="h-9 w-14 cursor-pointer rounded border border-gray-300 p-1"
                  />
                  <span className="font-mono text-sm text-gray-500">{color}</span>
                </div>
              </div>
              <div className="w-28 space-y-1">
                <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">Sort order</label>
                <Input
                  type="number"
                  value={sortOrder}
                  onChange={(e) => setSortOrder(Number(e.target.value))}
                />
              </div>
              <div className="flex gap-2">
                <Button
                  onClick={() => createMutation.mutate()}
                  disabled={!name.trim() || createMutation.isPending}
                >
                  {createMutation.isPending ? 'Adding…' : 'Add'}
                </Button>
                <Button variant="outline" onClick={() => { setAddingStatus(false); setName('') }}>
                  Cancel
                </Button>
              </div>
            </div>
            {formError && <p className="mt-2 text-sm text-red-600">{formError}</p>}
          </div>
        )}

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="overflow-hidden rounded-lg border bg-white">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs uppercase tracking-wide text-gray-500">
                <tr>
                  <th className="w-10 px-4 py-3 text-left">Color</th>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Type</th>
                  <th className="w-24 px-4 py-3 text-left">Sort order</th>
                  <th className="w-14 px-4 py-3" />
                </tr>
              </thead>
              <tbody className="divide-y">
                {sorted.map((s) => (
                  <tr key={s.id} className="group hover:bg-gray-50">
                    <td className="px-4 py-3">
                      <span
                        className="inline-block h-4 w-4 rounded-full border border-black/10 shadow-sm"
                        style={{ backgroundColor: s.color || DEFAULT_COLOR }}
                      />
                    </td>
                    <td className="px-4 py-3 font-medium text-gray-900">{s.name}</td>
                    <td className="px-4 py-3">
                      <Badge variant={s.kind === 'system' ? 'secondary' : 'outline'}>
                        {s.kind}
                      </Badge>
                    </td>
                    <td className="px-4 py-3 text-gray-500">{s.sort_order}</td>
                    <td className="px-4 py-3 text-right">
                      {s.kind === 'system' ? (
                        <LockIcon className="ml-auto h-3.5 w-3.5 text-gray-300" title="System status — cannot be deleted" />
                      ) : (
                        <button
                          onClick={() => deleteMutation.mutate(s.id)}
                          disabled={deleteMutation.isPending}
                          className="invisible text-gray-300 hover:text-red-500 group-hover:visible"
                          title="Delete status"
                        >
                          <TrashIcon className="h-4 w-4" />
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
                {sorted.length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-4 py-10 text-center text-sm text-gray-400">
                      No statuses configured.
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
