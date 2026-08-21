import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  listCannedResponses,
  createCannedResponse,
  updateCannedResponse,
  deleteCannedResponse,
  listCategories,
  listTypes,
} from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Select } from '@/components/ui/select'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon, PencilIcon, Trash2Icon } from 'lucide-react'
import type { Category, CannedResponse } from '@/api/types'

// ── Shared form state/fields ──────────────────────────────────────────────────

interface FormState {
  name: string
  body: string
  categoryId: string
  typeId: string
  sortOrder: string
}

const emptyForm: FormState = { name: '', body: '', categoryId: '', typeId: '', sortOrder: '0' }

function toInput(f: FormState) {
  return {
    name: f.name.trim(),
    body: f.body,
    category_id: f.categoryId || null,
    type_id: f.categoryId ? f.typeId || null : null,
    sort_order: Number(f.sortOrder) || 0,
  }
}

function CannedResponseFields({
  form,
  onChange,
  categories,
}: {
  form: FormState
  onChange: (f: FormState) => void
  categories: Category[]
}) {
  const { data: types = [] } = useQuery({
    queryKey: ['admin', 'types', form.categoryId],
    queryFn: () => listTypes(form.categoryId),
    enabled: !!form.categoryId,
  })

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-3 gap-3">
        <div className="col-span-2 space-y-1">
          <Label className="text-xs">Name</Label>
          <Input
            placeholder="e.g. Password reset instructions"
            value={form.name}
            onChange={(e) => onChange({ ...form, name: e.target.value })}
          />
        </div>
        <div className="space-y-1">
          <Label className="text-xs">Sort order</Label>
          <Input
            type="number"
            value={form.sortOrder}
            onChange={(e) => onChange({ ...form, sortOrder: e.target.value })}
          />
        </div>
      </div>
      <div className="space-y-1">
        <Label className="text-xs">Body</Label>
        <Textarea
          placeholder="Reply text staff can insert into a ticket reply"
          rows={4}
          value={form.body}
          onChange={(e) => onChange({ ...form, body: e.target.value })}
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label className="text-xs">Category</Label>
          <Select
            value={form.categoryId}
            onChange={(e) => onChange({ ...form, categoryId: e.target.value, typeId: '' })}
          >
            <option value="">Global (all categories)</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>{c.name}</option>
            ))}
          </Select>
        </div>
        <div className="space-y-1">
          <Label className="text-xs">Type</Label>
          <Select
            value={form.typeId}
            onChange={(e) => onChange({ ...form, typeId: e.target.value })}
            disabled={!form.categoryId}
          >
            <option value="">Whole category (all types)</option>
            {types.map((t) => (
              <option key={t.id} value={t.id}>{t.name}</option>
            ))}
          </Select>
        </div>
      </div>
    </div>
  )
}

// ── Edit row ───────────────────────────────────────────────────────────────────

function EditRow({
  response,
  categories,
  onCancel,
}: {
  response: CannedResponse
  categories: Category[]
  onCancel: () => void
}) {
  const qc = useQueryClient()
  const [form, setForm] = useState<FormState>({
    name: response.name,
    body: response.body,
    categoryId: response.category_id ?? '',
    typeId: response.type_id ?? '',
    sortOrder: String(response.sort_order),
  })
  const [error, setError] = useState('')

  const updateMutation = useMutation({
    mutationFn: () => updateCannedResponse(response.id, toInput(form)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'canned-responses'] })
      onCancel()
    },
    onError: (err) => setError(extractError(err)),
  })

  return (
    <tr>
      <td colSpan={5} className="px-4 py-4 bg-gray-50">
        <div className="space-y-3">
          <CannedResponseFields form={form} onChange={setForm} categories={categories} />
          {error && <p className="text-sm text-red-600">{error}</p>}
          <div className="flex gap-2">
            <Button
              size="sm"
              onClick={() => updateMutation.mutate()}
              disabled={!form.name.trim() || updateMutation.isPending}
            >
              {updateMutation.isPending ? 'Saving…' : 'Save'}
            </Button>
            <Button size="sm" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </div>
      </td>
    </tr>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export function CannedResponsesPage() {
  const qc = useQueryClient()
  const [form, setForm] = useState<FormState>(emptyForm)
  const [createError, setCreateError] = useState('')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<CannedResponse | null>(null)

  const { data: responses = [], isLoading } = useQuery({
    queryKey: ['admin', 'canned-responses'],
    queryFn: listCannedResponses,
  })

  const { data: categories = [] } = useQuery({
    queryKey: ['admin', 'categories'],
    queryFn: listCategories,
  })

  // Resolve type names for the scope column. We only need types for the
  // categories actually referenced by a category+type-scoped response, so
  // fetch just those rather than every type in the system.
  const categoryIdsNeedingTypes = Array.from(
    new Set(responses.filter((r) => r.category_id && r.type_id).map((r) => r.category_id as string))
  ).sort()

  const { data: typesByCategory = {} } = useQuery({
    queryKey: ['admin', 'canned-responses', 'types-by-category', categoryIdsNeedingTypes],
    queryFn: async () => {
      const entries = await Promise.all(
        categoryIdsNeedingTypes.map(async (id) => [id, await listTypes(id)] as const)
      )
      return Object.fromEntries(entries)
    },
    enabled: categoryIdsNeedingTypes.length > 0,
  })

  const categoryName = (id?: string) => categories.find((c) => c.id === id)?.name ?? id
  const typeName = (categoryId: string, typeId: string) =>
    typesByCategory[categoryId]?.find((t) => t.id === typeId)?.name ?? typeId

  function scopeLabel(r: CannedResponse): string {
    if (!r.category_id) return 'Global'
    if (!r.type_id) return categoryName(r.category_id) ?? r.category_id
    return `${categoryName(r.category_id)} > ${typeName(r.category_id, r.type_id)}`
  }

  function bodyPreview(body: string): string {
    const oneLine = body.replace(/\s+/g, ' ').trim()
    return oneLine.length > 80 ? `${oneLine.slice(0, 80)}…` : oneLine
  }

  const createMutation = useMutation({
    mutationFn: () => createCannedResponse(toInput(form)),
    onSuccess: () => {
      setForm(emptyForm)
      setCreateError('')
      qc.invalidateQueries({ queryKey: ['admin', 'canned-responses'] })
    },
    onError: (err) => setCreateError(extractError(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteCannedResponse(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'canned-responses'] })
      setPendingDelete(null)
    },
  })

  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Canned Responses</h1>
          <p className="mt-1 text-sm text-gray-500">
            Reusable reply templates staff insert into ticket replies. Admins manage them here.
          </p>
        </div>

        {/* Create form */}
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Add canned response</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <CannedResponseFields form={form} onChange={setForm} categories={categories} />
            {createError && <p className="text-sm text-red-600">{createError}</p>}
            <Button
              size="sm"
              onClick={() => createMutation.mutate()}
              disabled={!form.name.trim() || createMutation.isPending}
            >
              <PlusIcon className="mr-2 h-4 w-4" />
              {createMutation.isPending ? 'Adding…' : 'Add'}
            </Button>
          </CardContent>
        </Card>

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="rounded-lg border bg-white overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
                <tr>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Body</th>
                  <th className="px-4 py-3 text-left">Scope</th>
                  <th className="px-4 py-3 text-left">Sort order</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {responses.map((r) =>
                  editingId === r.id ? (
                    <EditRow
                      key={r.id}
                      response={r}
                      categories={categories}
                      onCancel={() => setEditingId(null)}
                    />
                  ) : (
                    <tr key={r.id}>
                      <td className="px-4 py-3 font-medium text-gray-900">{r.name}</td>
                      <td className="px-4 py-3 text-gray-500">{bodyPreview(r.body)}</td>
                      <td className="px-4 py-3 text-gray-500">{scopeLabel(r)}</td>
                      <td className="px-4 py-3 text-gray-500">{r.sort_order}</td>
                      <td className="px-4 py-3 text-right">
                        <div className="flex justify-end gap-2">
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => setEditingId(r.id)}
                          >
                            <PencilIcon className="mr-1 h-3.5 w-3.5" />
                            Edit
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            className="text-red-600 border-red-200 hover:bg-red-50"
                            onClick={() => setPendingDelete(r)}
                          >
                            <Trash2Icon className="mr-1 h-3.5 w-3.5" />
                            Delete
                          </Button>
                        </div>
                      </td>
                    </tr>
                  )
                )}
                {responses.length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-4 py-8 text-center text-gray-400">
                      No canned responses yet.
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
        title={`Delete canned response "${pendingDelete?.name ?? ''}"?`}
        description="Staff will no longer be able to insert this response into replies."
        confirmLabel="Delete"
        isPending={deleteMutation.isPending}
        onConfirm={() => { if (pendingDelete) deleteMutation.mutate(pendingDelete.id) }}
      />
    </Layout>
  )
}
