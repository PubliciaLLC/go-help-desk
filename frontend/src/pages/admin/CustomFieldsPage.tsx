import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listFieldDefs, createFieldDef, updateFieldDef } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon } from 'lucide-react'
import type { FieldDef, FieldType } from '@/api/types'

const FIELD_TYPES: { value: FieldType; label: string }[] = [
  { value: 'text', label: 'Text' },
  { value: 'textarea', label: 'Text area' },
  { value: 'number', label: 'Number' },
  { value: 'select', label: 'Select (dropdown)' },
]

export function CustomFieldsPage() {
  const qc = useQueryClient()
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [fieldType, setFieldType] = useState<FieldType>('text')
  const [optionsRaw, setOptionsRaw] = useState('') // comma-separated
  const [sortOrder, setSortOrder] = useState(0)
  const [formError, setFormError] = useState('')

  const { data: defs = [], isLoading } = useQuery({
    queryKey: ['admin', 'custom-fields'],
    queryFn: listFieldDefs,
  })

  const createMutation = useMutation({
    mutationFn: () => {
      const options = fieldType === 'select'
        ? optionsRaw.split(',').map(s => s.trim()).filter(Boolean)
        : undefined
      return createFieldDef({ name: name.trim(), field_type: fieldType, options, sort_order: sortOrder })
    },
    onSuccess: () => {
      resetForm()
      qc.invalidateQueries({ queryKey: ['admin', 'custom-fields'] })
    },
    onError: (err) => setFormError(extractError(err)),
  })

  const deactivateMutation = useMutation({
    mutationFn: (id: string) => updateFieldDef(id, { active: false }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'custom-fields'] }),
  })

  const reactivateMutation = useMutation({
    mutationFn: (id: string) => updateFieldDef(id, { active: true }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'custom-fields'] }),
  })

  function resetForm() {
    setName('')
    setFieldType('text')
    setOptionsRaw('')
    setSortOrder(0)
    setFormError('')
    setAdding(false)
  }

  function labelForType(ft: FieldType) {
    return FIELD_TYPES.find(t => t.value === ft)?.label ?? ft
  }

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900">Custom Fields</h1>
            <p className="mt-1 text-sm text-gray-500">
              Define reusable fields that can be assigned to categories, types, and items. Once created, fields cannot be deleted — only deactivated.
            </p>
          </div>
          <Button onClick={() => setAdding(true)} className="ml-6 shrink-0">
            <PlusIcon className="mr-2 h-4 w-4" />
            New Field
          </Button>
        </div>

        {adding && (
          <div className="rounded-lg border bg-white p-4 space-y-3">
            <p className="text-sm font-medium text-gray-700">New custom field</p>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1">
                <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">Name</label>
                <Input
                  autoFocus
                  placeholder="e.g. Asset Tag"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Escape') resetForm() }}
                />
              </div>
              <div className="space-y-1">
                <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">Type</label>
                <select
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  value={fieldType}
                  onChange={(e) => setFieldType(e.target.value as FieldType)}
                >
                  {FIELD_TYPES.map(t => (
                    <option key={t.value} value={t.value}>{t.label}</option>
                  ))}
                </select>
              </div>
            </div>
            {fieldType === 'select' && (
              <div className="space-y-1">
                <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">
                  Options <span className="normal-case font-normal">(comma-separated)</span>
                </label>
                <Input
                  placeholder="e.g. IT, HR, Finance"
                  value={optionsRaw}
                  onChange={(e) => setOptionsRaw(e.target.value)}
                />
              </div>
            )}
            <div className="w-36 space-y-1">
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
                {createMutation.isPending ? 'Adding…' : 'Add Field'}
              </Button>
              <Button variant="outline" onClick={resetForm}>Cancel</Button>
            </div>
            {formError && <p className="text-sm text-red-600">{formError}</p>}
          </div>
        )}

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="overflow-hidden rounded-lg border bg-white">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs uppercase tracking-wide text-gray-500">
                <tr>
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Type</th>
                  <th className="px-4 py-3 text-left">Options</th>
                  <th className="w-24 px-4 py-3 text-left">Sort</th>
                  <th className="px-4 py-3 text-left">Status</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {defs.map((def: FieldDef) => (
                  <tr key={def.id} className={`group hover:bg-gray-50 ${!def.active ? 'opacity-50' : ''}`}>
                    <td className="px-4 py-3 font-medium text-gray-900">{def.name}</td>
                    <td className="px-4 py-3 text-gray-600">{labelForType(def.field_type)}</td>
                    <td className="px-4 py-3 text-gray-500 text-xs">
                      {def.field_type === 'select' && def.options?.length
                        ? def.options.join(', ')
                        : <span className="text-gray-300">—</span>}
                    </td>
                    <td className="px-4 py-3 text-gray-500">{def.sort_order}</td>
                    <td className="px-4 py-3">
                      <Badge variant={def.active ? 'default' : 'secondary'}>
                        {def.active ? 'Active' : 'Inactive'}
                      </Badge>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-end gap-2">
                        {def.active ? (
                          <Button
                            size="sm"
                            variant="outline"
                            className="text-yellow-700 border-yellow-300 hover:bg-yellow-50"
                            onClick={() => deactivateMutation.mutate(def.id)}
                            disabled={deactivateMutation.isPending}
                          >
                            Deactivate
                          </Button>
                        ) : (
                          <Button
                            size="sm"
                            variant="outline"
                            className="text-green-700 border-green-300 hover:bg-green-50"
                            onClick={() => reactivateMutation.mutate(def.id)}
                            disabled={reactivateMutation.isPending}
                          >
                            Reactivate
                          </Button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
                {defs.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-4 py-10 text-center text-sm text-gray-400">
                      No custom fields defined yet.
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
