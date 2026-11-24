import { useEffect, useId, useRef, useState } from 'react'
import { useMutation, useMutationState, useQuery, useQueryClient } from '@tanstack/react-query'
import { createSLAPolicy, deleteSLAPolicy, listCategories, listSLAPolicies, updateSLAPolicy } from '@/api/admin'
import type { Priority, SLAPolicy } from '@/api/types'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { fmtMin } from '@/lib/format'
import { cn } from '@/lib/utils'

// ── SLA policies blade ────────────────────────────────────────────────────────

const PRIORITIES = ['critical', 'high', 'medium', 'low'] as const
const PRIORITY_COLORS: Record<string, string> = {
  critical: 'bg-red-100 text-red-700',
  high: 'bg-orange-100 text-orange-700',
  medium: 'bg-yellow-100 text-yellow-700',
  low: 'bg-blue-100 text-blue-700',
}

type PolicyForm = {
  name: string
  priority: string
  category_id: string
  response_target_min: number
  resolution_target_min: number
}

const EMPTY_FORM: PolicyForm = {
  name: '',
  priority: 'medium',
  category_id: '',
  response_target_min: 480,
  resolution_target_min: 2880,
}

// The stacked add and edit form. It is a block outside the policy table (the
// table scrolls sideways in a narrow card, and a form inside it would too), so
// it lays out at the card's width. Name is full width. Priority and Category,
// and Response and Resolution, sit side by side from the sm breakpoint (640px)
// up and stack in one column below it, which includes 375px. Buttons underneath.
function PolicyFormFields({
  form, setForm, categories, onSave, onCancel, nameRef,
}: {
  nameRef: React.Ref<HTMLInputElement>
  form: PolicyForm
  setForm: React.Dispatch<React.SetStateAction<PolicyForm>>
  categories: { id: string; name: string; active: boolean }[]
  onSave: () => void
  onCancel: () => void
}) {
  const id = useId()
  return (
    <div className="space-y-3 rounded border bg-blue-50 p-3">
      <div className="space-y-1">
        <label htmlFor={`${id}-name`} className="block text-sm font-medium text-gray-900">Policy name</label>
        <Input
          id={`${id}-name`}
          ref={nameRef}
          className="h-9 text-sm"
          value={form.name}
          onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
        />
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <label htmlFor={`${id}-priority`} className="block text-sm font-medium text-gray-900">Priority</label>
          <Select
            id={`${id}-priority`}
            className="h-9 text-sm"
            value={form.priority}
            onChange={(e) => setForm((f) => ({ ...f, priority: e.target.value }))}
          >
            <option value="">Any priority</option>
            {PRIORITIES.map((p) => (
              <option key={p} value={p} className="capitalize">{p}</option>
            ))}
          </Select>
        </div>
        <div className="space-y-1">
          <label htmlFor={`${id}-category`} className="block text-sm font-medium text-gray-900">Category</label>
          <Select
            id={`${id}-category`}
            className="h-9 text-sm"
            value={form.category_id}
            onChange={(e) => setForm((f) => ({ ...f, category_id: e.target.value }))}
          >
            <option value="">All categories</option>
            {categories.filter((c) => c.active).map((c) => (
              <option key={c.id} value={c.id}>{c.name}</option>
            ))}
          </Select>
        </div>
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <label htmlFor={`${id}-response`} className="block text-sm font-medium text-gray-900">Response target</label>
          <div className="flex items-center gap-2">
            <Input
              id={`${id}-response`}
              type="number" min={1} className="h-9 w-24 text-sm"
              value={form.response_target_min}
              onChange={(e) => setForm((f) => ({ ...f, response_target_min: Math.max(1, parseInt(e.target.value, 10) || 1) }))}
            />
            <span className="text-xs text-gray-500">min</span>
          </div>
        </div>
        <div className="space-y-1">
          <label htmlFor={`${id}-resolution`} className="block text-sm font-medium text-gray-900">Resolution target</label>
          <div className="flex items-center gap-2">
            <Input
              id={`${id}-resolution`}
              type="number" min={1} className="h-9 w-24 text-sm"
              value={form.resolution_target_min}
              onChange={(e) => setForm((f) => ({ ...f, resolution_target_min: Math.max(1, parseInt(e.target.value, 10) || 1) }))}
            />
            <span className="text-xs text-gray-500">min</span>
          </div>
        </div>
      </div>
      <div className="flex gap-2">
        <Button size="sm" onClick={onSave}>Save</Button>
        <Button size="sm" variant="outline" onClick={onCancel}>Cancel</Button>
      </div>
    </div>
  )
}

// One add or edit of a policy, as the form held it when Save was pressed.
// id is the policy being edited, or null for an add.
type SaveVars = { id: string | null; form: PolicyForm }

const SLA_KEY = ['sla-policies']
const SAVE_KEY = ['sla-policy-save']
// Keys for the temporary rows of adds the server has not answered yet.
const TEMP_PREFIX = 'saving-'

function rowFromForm(id: string, f: PolicyForm): SLAPolicy {
  return {
    id,
    name: f.name,
    priority: (f.priority || undefined) as Priority | undefined,
    category_id: f.category_id || undefined,
    response_target_min: f.response_target_min,
    resolution_target_min: f.resolution_target_min,
  }
}

// What the table shows: a server row, or an overlay of a save still in flight.
type Row = { policy: SLAPolicy; saving: boolean; unsaved: boolean }

// Saves are asynchronous (#672): Save closes the form at once and the request
// runs on its own. Nothing is written to the query cache for a save. The table
// is the server's list with an overlay of the saves in flight, and both the
// overlay and the failures are read from react-query's mutation cache, which
// outlives this component: leaving the tab and coming back changes nothing.
// A save is held pending until the refetch it triggers has landed, so its row
// is not dropped before the list has caught up. A failed save stays in the
// mutation cache, listed above the table with Reopen, until it is reopened or
// dismissed. Nothing a save does when it finishes touches the open form.
export function SLAPoliciesSection() {
  const qc = useQueryClient()
  const [editingId, setEditingId] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<SLAPolicy | null>(null)
  const [showAdd, setShowAdd] = useState(false)
  const [form, setForm] = useState<PolicyForm>(EMPTY_FORM)
  const [formError, setFormError] = useState('')
  const nameRef = useRef<HTMLInputElement>(null)

  // The form renders below the whole table, so on opening it (or switching it
  // to another policy) move focus to its Name field and bring it into view.
  // Keyed on what is open, not on the form values, so typing does not re-run it.
  useEffect(() => {
    if (!showAdd && editingId === null) return
    nameRef.current?.focus()
    nameRef.current?.scrollIntoView?.({ block: 'nearest' })
  }, [showAdd, editingId])

  const { data: policies = [] } = useQuery({ queryKey: SLA_KEY, queryFn: listSLAPolicies })
  const { data: categories = [] } = useQuery({ queryKey: ['categories'], queryFn: listCategories })

  const inFlight = useMutationState({
    filters: { mutationKey: SAVE_KEY, status: 'pending' },
    select: (m) => ({ key: m.mutationId, vars: m.state.variables as SaveVars }),
  })
  const failed = useMutationState({
    filters: { mutationKey: SAVE_KEY, status: 'error' },
    select: (m) => ({ key: m.mutationId, vars: m.state.variables as SaveVars, error: m.state.error }),
  })

  // Last writer wins: of several saves of one policy, the latest shows.
  const ordered = [...inFlight].sort((a, b) => a.key - b.key)
  const edits = new Map(ordered.filter((m) => m.vars.id !== null).map((m) => [m.vars.id, m.vars.form]))
  const rows: Row[] = [
    ...policies.map((p) => {
      const f = edits.get(p.id)
      return { policy: f ? rowFromForm(p.id, f) : p, saving: f !== undefined, unsaved: false }
    }),
    ...ordered
      .filter((m) => m.vars.id === null)
      .map((m) => ({ policy: rowFromForm(`${TEMP_PREFIX}${m.key}`, m.vars.form), saving: true, unsaved: true })),
  ]

  function startEdit(p: SLAPolicy) {
    setEditingId(p.id)
    setShowAdd(false)
    setFormError('')
    setForm({
      name: p.name,
      priority: p.priority ?? '',
      category_id: p.category_id ?? '',
      response_target_min: p.response_target_min,
      resolution_target_min: p.resolution_target_min,
    })
  }

  function startAdd() {
    setShowAdd(true)
    setEditingId(null)
    setFormError('')
    setForm(EMPTY_FORM)
  }

  function discard(key: number) {
    const cache = qc.getMutationCache()
    const m = cache.getAll().find((x) => x.mutationId === key)
    if (m) cache.remove(m)
  }

  function reopen(f: (typeof failed)[number]) {
    discard(f.key)
    setFormError('')
    setForm(f.vars.form)
    setEditingId(f.vars.id)
    setShowAdd(f.vars.id === null)
  }

  const saveMutation = useMutation({
    mutationKey: SAVE_KEY,
    // Failed saves stay in the mutation cache: they are the error list, and
    // they must outlive this component.
    gcTime: Infinity,
    mutationFn: async ({ id, form: f }: SaveVars) => {
      if (id === null) {
        await createSLAPolicy({
          name: f.name,
          priority: f.priority || undefined,
          category_id: f.category_id || undefined,
          response_target_min: f.response_target_min,
          resolution_target_min: f.resolution_target_min,
        })
      } else {
        await updateSLAPolicy(id, {
          name: f.name,
          priority: f.priority || undefined,
          clear_priority: !f.priority,
          category_id: f.category_id || undefined,
          clear_category: !f.category_id,
          response_target_min: f.response_target_min,
          resolution_target_min: f.resolution_target_min,
        })
      }
      // Stay pending until the refetch lands, so the row keeps its marker
      // until the real one is in the list.
      await qc.invalidateQueries({ queryKey: SLA_KEY })
    },
    // A save that worked has nothing left to show (the list has caught up), so
    // it leaves the cache, which then holds only saves pending or failed. The
    // variables object is the one handed to mutate(), so it identifies this one.
    onSuccess: (_data, vars) => {
      const cache = qc.getMutationCache()
      const self = cache.find({ mutationKey: SAVE_KEY, predicate: (m) => m.state.variables === vars })
      if (self) cache.remove(self)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteSLAPolicy(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: SLA_KEY })
      setPendingDelete(null)
      setFormError('')
    },
    onError: (err) => {
      setPendingDelete(null)
      setFormError(extractError(err))
    },
  })

  return (
    <div className="border-t bg-gray-50">
      <div className="px-5 pt-3 pb-0">
        <p className="text-xs font-medium uppercase tracking-wider text-gray-400">SLA Policies</p>
      </div>
      <div className="px-5 py-4 space-y-3">
        {failed.map((f) => {
          const name = f.vars.form.name || 'unnamed policy'
          return (
            <div key={f.key} className="flex items-start justify-between gap-3 rounded border border-red-200 bg-red-50 p-3 text-sm text-red-700">
              <span role="alert">Saving '{name}' failed: {extractError(f.error)}</span>
              <span className="flex shrink-0 gap-3">
                <button aria-label={`Reopen '${name}'`} className="text-xs font-medium text-red-700 underline" onClick={() => reopen(f)}>Reopen</button>
                <button aria-label={`Dismiss '${name}'`} className="text-xs text-red-700 hover:underline" onClick={() => discard(f.key)}>Dismiss</button>
              </span>
            </div>
          )
        })}
        {rows.length > 0 ? (
          <div className="overflow-x-auto rounded border bg-white">
            <table className="w-full text-sm">
              <thead className="border-b bg-gray-50">
                <tr className="text-left text-xs font-medium text-gray-500">
                  <th className="px-3 py-2">Name</th>
                  <th className="px-3 py-2">Priority</th>
                  <th className="px-3 py-2">Category</th>
                  <th className="px-3 py-2">Response</th>
                  <th className="px-3 py-2">Resolution</th>
                  <th className="px-3 py-2"></th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {rows.map(({ policy: p, saving, unsaved }) => {
                  const isEditing = editingId === p.id
                  const marked = isEditing || saving
                  return (
                    <tr key={p.id} aria-current={marked ? 'true' : undefined} className={marked ? 'bg-blue-50' : 'hover:bg-gray-50'}>
                      <td className="px-3 py-2 font-medium">
                        {p.name}
                        {isEditing && <span className="ml-2 text-xs font-normal text-blue-700">Editing</span>}
                        {saving && <span className="ml-2 text-xs font-normal text-blue-700">Saving…</span>}
                      </td>
                      <td className="px-3 py-2">
                        {p.priority ? (
                          <span className={cn('rounded-full px-2 py-0.5 text-xs font-medium capitalize', PRIORITY_COLORS[p.priority])}>
                            {p.priority}
                          </span>
                        ) : (
                          <span className="text-gray-600">Any priority</span>
                        )}
                      </td>
                      <td className="px-3 py-2 text-gray-600">
                        {p.category_id
                          ? (categories.find((c) => c.id === p.category_id)?.name ?? '—')
                          : 'All categories'}
                      </td>
                      <td className="px-3 py-2 tabular-nums text-gray-600">{fmtMin(p.response_target_min)}</td>
                      <td className="px-3 py-2 tabular-nums text-gray-600">{fmtMin(p.resolution_target_min)}</td>
                      <td className="px-3 py-2">
                        {!isEditing && !unsaved && (
                          <div className="flex gap-3">
                            <button className="text-xs text-blue-600 hover:underline" onClick={() => startEdit(p)}>Edit</button>
                            <button
                              className="text-xs text-red-600 hover:underline disabled:opacity-40"
                              onClick={() => { setFormError(''); setPendingDelete(p) }}
                              disabled={deleteMutation.isPending}
                            >
                              Delete
                            </button>
                          </div>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        ) : (
          !showAdd && editingId === null && <p className="text-sm text-gray-500">No SLA policies defined.</p>
        )}
        {(showAdd || editingId !== null) && (
          <PolicyFormFields
            form={form} setForm={setForm} categories={categories} nameRef={nameRef}
            onSave={() => {
              saveMutation.mutate({ id: editingId, form })
              if (editingId !== null) {
                setEditingId(null)
              } else {
                setShowAdd(false)
                setForm(EMPTY_FORM)
              }
            }}
            onCancel={() => { setFormError(''); setShowAdd(false); setEditingId(null) }}
          />
        )}
        {formError && <p role="alert" className="text-sm text-red-600">{formError}</p>}
        {!showAdd && editingId === null && (
          <Button size="sm" variant="outline" onClick={startAdd}>+ Add policy</Button>
        )}
      </div>
      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => { if (!open) setPendingDelete(null) }}
        title={`Delete SLA policy "${pendingDelete?.name ?? ''}"?`}
        description="This cannot be undone. A policy that any ticket has been tracked against cannot be deleted."
        confirmLabel="Delete policy"
        isPending={deleteMutation.isPending}
        onConfirm={() => { if (pendingDelete) deleteMutation.mutate(pendingDelete.id) }}
      />
    </div>
  )
}
