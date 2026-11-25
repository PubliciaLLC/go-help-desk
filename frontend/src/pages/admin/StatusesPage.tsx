import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listStatuses, createStatus, updateStatus, deleteStatus } from '@/api/admin'
import type { Status } from '@/api/types'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon, LockIcon } from 'lucide-react'

const DEFAULT_COLOR = '#6b7280' // theme-ok: stored status data, not styling

export function StatusesPage() {
  const qc = useQueryClient()
  const [addingStatus, setAddingStatus] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<Status | null>(null)
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

  const deactivateMutation = useMutation({
    mutationFn: (id: string) => updateStatus(id, { active: false }),
    onSuccess: () => {
      setFormError('')
      qc.invalidateQueries({ queryKey: ['admin', 'statuses'] })
    },
  })

  const reactivateMutation = useMutation({
    mutationFn: (id: string) => updateStatus(id, { active: true }),
    onSuccess: () => {
      setFormError('')
      qc.invalidateQueries({ queryKey: ['admin', 'statuses'] })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteStatus(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'statuses'] })
      setPendingDelete(null)
      setFormError('')
    },
    onError: (err) => {
      setPendingDelete(null)
      setFormError(extractError(err))
    },
  })

  const sorted = [...statuses].sort((a, b) => a.sort_order - b.sort_order)

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex flex-col items-start gap-3 sm:flex-row sm:justify-between sm:gap-0">
          <div className="min-w-0">
            <h1 className="text-2xl font-bold text-fg">Ticket Statuses</h1>
            <p className="mt-1 text-sm text-fg-faint">
              Add custom intermediate statuses for your workflow. The three system statuses — New, Resolved, and Closed — have fixed lifecycle rules and cannot be renamed, removed, or deactivated.
            </p>
          </div>
          <Button onClick={() => { setFormError(''); setAddingStatus(true) }} className="shrink-0 sm:ml-6">
            <PlusIcon className="mr-2 h-4 w-4" />
            New Status
          </Button>
        </div>

        {addingStatus && (
          <div className="rounded-lg border bg-card p-4">
            <p className="mb-3 text-sm font-medium text-fg-soft">New status</p>
            <div className="flex items-end gap-3">
              <div className="flex-1 space-y-1">
                <label className="text-xs font-medium text-fg-faint uppercase tracking-wide">Name</label>
                <Input
                  autoFocus
                  placeholder="e.g. In Progress"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && name.trim()) createMutation.mutate()
                    if (e.key === 'Escape') { setAddingStatus(false); setName(''); setFormError('') }
                  }}
                />
              </div>
              <div className="space-y-1">
                <label className="text-xs font-medium text-fg-faint uppercase tracking-wide">Color</label>
                <div className="flex items-center gap-2 h-9">
                  <input
                    type="color"
                    value={color}
                    onChange={(e) => setColor(e.target.value)}
                    className="h-9 w-14 cursor-pointer rounded border border-border-strong p-1"
                  />
                  <span className="font-mono text-sm text-fg-faint">{color}</span>
                </div>
              </div>
              <div className="w-28 space-y-1">
                <label className="text-xs font-medium text-fg-faint uppercase tracking-wide">Sort order</label>
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
                <Button variant="outline" onClick={() => { setAddingStatus(false); setName(''); setFormError('') }}>
                  Cancel
                </Button>
              </div>
            </div>
            {formError && <p role="alert" className="mt-2 text-sm text-danger-fg">{formError}</p>}
          </div>
        )}

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="overflow-x-auto rounded-lg border bg-card">
            <table className="w-full text-sm">
              <thead className="bg-subtle text-xs uppercase tracking-wide text-fg-faint">
                <tr>
                  <th className="w-10 px-4 py-3 text-left">Color</th>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Type</th>
                  <th className="w-24 px-4 py-3 text-left">Sort order</th>
                  <th className="px-4 py-3 text-left">Tickets</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {sorted.map((s) => (
                  <tr key={s.id} className={`group hover:bg-subtle ${!s.active ? 'opacity-50' : ''}`}>
                    <td className="px-4 py-3">
                      <span
                        className="inline-block h-4 w-4 rounded-full border border-border shadow-sm"
                        style={{ backgroundColor: s.color || DEFAULT_COLOR }}
                      />
                    </td>
                    <td className="px-4 py-3 font-medium text-fg wrap-anywhere">
                      {s.name}
                      {!s.active && (
                        <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-xs font-normal text-fg-muted">
                          inactive
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <Badge variant={s.kind === 'system' ? 'secondary' : 'outline'}>
                        {s.kind}
                      </Badge>
                    </td>
                    <td className="px-4 py-3 text-fg-faint">{s.sort_order}</td>
                    <td className="px-4 py-3 text-fg-faint">{s.ticket_count}</td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-end gap-2">
                        {s.kind === 'system' ? (
                          <span title="System status — cannot be modified" className="flex justify-end">
                            <LockIcon className="h-3.5 w-3.5 text-fg-faint" />
                          </span>
                        ) : s.active ? (
                          <Button
                            size="sm"
                            variant="outline"
                            className="text-warning-fg border-warning-border hover:bg-warning-soft hover:text-warning-fg-strong"
                            onClick={() => deactivateMutation.mutate(s.id)}
                            disabled={deactivateMutation.isPending}
                          >
                            Deactivate
                          </Button>
                        ) : (
                          <>
                            <Button
                              size="sm"
                              variant="outline"
                              className="text-success-fg border-success-border hover:bg-success-soft hover:text-success-fg-strong"
                              onClick={() => reactivateMutation.mutate(s.id)}
                              disabled={reactivateMutation.isPending}
                            >
                              Reactivate
                            </Button>
                            {s.ticket_count === 0 && (
                              <Button
                                size="sm"
                                variant="outline"
                                className="text-danger-fg border-danger-border hover:bg-danger-soft hover:text-danger-fg-strong"
                                onClick={() => { setFormError(''); setPendingDelete(s) }}
                                disabled={deleteMutation.isPending}
                              >
                                Delete
                              </Button>
                            )}
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
                {sorted.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-4 py-10 text-center text-sm text-fg-faint">
                      No statuses configured.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
        {/* Not nested under addingStatus above: this also carries delete
            refusals (409 status_in_use, #275) surfaced outside that form. */}
        {formError && !addingStatus && <p className="text-sm text-danger-fg">{formError}</p>}
      </div>
      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => { if (!open) setPendingDelete(null) }}
        title={`Delete status "${pendingDelete?.name ?? ''}"?`}
        description="This status has no tickets and will be permanently removed."
        confirmLabel="Delete status"
        isPending={deleteMutation.isPending}
        onConfirm={() => { if (pendingDelete) deleteMutation.mutate(pendingDelete.id) }}
      />
    </Layout>
  )
}
