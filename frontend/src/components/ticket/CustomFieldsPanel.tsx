import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listTicketCustomFields, putTicketCustomFields, resolveFieldsForCTI } from '@/api/tickets'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { FieldType, TicketFieldValue } from '@/api/types'

interface CustomFieldsPanelProps {
  ticketId: string
  categoryId: string
  typeId?: string
  itemId?: string
  isStaffOrAdmin: boolean
}

// One line of the card. `editable` is the whole rule for what edit mode offers
// and what Save sends, so an inactive field can never end up in the request.
interface Row {
  id: string
  name: string
  type: FieldType
  options?: string[]
  value: string
  editable: boolean
  inactive: boolean
}

export function CustomFieldsPanel({
  ticketId,
  categoryId,
  typeId,
  itemId,
  isStaffOrAdmin,
}: CustomFieldsPanelProps) {
  const qc = useQueryClient()
  const { data: values, isLoading: valuesLoading } = useQuery<TicketFieldValue[]>({
    queryKey: ['customFields', ticketId],
    queryFn: () => listTicketCustomFields(ticketId),
  })
  // The values list holds only fields with a value. The ticket's own fields, and
  // the order they appear in, come from its classification, the same call the
  // new-ticket form makes.
  const { data: resolved, isLoading: resolvedLoading } = useQuery({
    queryKey: ['ctiFields', categoryId, typeId, itemId],
    queryFn: () => resolveFieldsForCTI({ category_id: categoryId, type_id: typeId, item_id: itemId }),
  })

  const [editValues, setEditValues] = useState<Record<string, string> | null>(null)
  const [saveError, setSaveError] = useState('')

  const saveMutation = useMutation({
    mutationFn: () => putTicketCustomFields(ticketId, editValues!),
    onSuccess: () => {
      setEditValues(null)
      setSaveError('')
      qc.invalidateQueries({ queryKey: ['customFields', ticketId] })
    },
    onError: (err) => setSaveError(extractError(err)),
  })

  // Wait for both, so staff do not see the card fill in field by field. A failed
  // resolve leaves `resolved` undefined and the card falls back to the values.
  if (valuesLoading || resolvedLoading) return null

  const held = new Map((values ?? []).map((v) => [v.field_def_id, v]))
  const rows: Row[] = []
  const placed = new Set<string>()

  // The resolver already returns category, then type, then item. Keep that order.
  for (const a of resolved ?? []) {
    // Skip a field attached at two levels after its first appearance; React
    // needs unique keys and the field is one value on the ticket.
    if (placed.has(a.field_def_id)) continue
    const v = held.get(a.field_def_id)
    const def = a.field_def
    const active = def ? def.active : true
    const value = v?.value ?? ''
    // Requesters see what holds a value. Staff also see active fields that are
    // empty, because that is the only way to fill them in; an inactive field
    // with no value has nothing to show or to do.
    if (!value && !(isStaffOrAdmin && active)) continue
    placed.add(a.field_def_id)
    rows.push({
      id: a.field_def_id,
      name: def?.name ?? v?.field_name ?? '',
      type: def?.field_type ?? v?.field_type ?? 'text',
      options: def?.options ?? v?.options,
      value,
      editable: isStaffOrAdmin && active,
      inactive: !active,
    })
  }
  // A value the ticket's classification no longer attaches comes last, in the
  // order the server returned it, and cannot be edited: the server refuses a
  // write to a field the ticket does not have.
  for (const v of values ?? []) {
    if (placed.has(v.field_def_id)) continue
    rows.push({
      id: v.field_def_id,
      name: v.field_name,
      type: v.field_type,
      options: v.options,
      value: v.value,
      editable: false,
      inactive: false,
    })
  }

  if (rows.length === 0) return null

  const canEdit = rows.some((r) => r.editable)

  function renderInput(r: Row) {
    const id = `custom-field-${r.id}`
    const current = editValues?.[r.id] ?? r.value
    const onChange = (val: string) =>
      setEditValues((prev) => ({ ...(prev ?? {}), [r.id]: val }))

    switch (r.type) {
      case 'textarea':
        return (
          <Textarea
            id={id}
            value={current}
            onChange={(e) => onChange(e.target.value)}
            rows={2}
            className="text-xs"
          />
        )
      case 'number':
        return (
          <Input
            id={id}
            type="number"
            value={current}
            onChange={(e) => onChange(e.target.value)}
            className="h-7 text-xs"
          />
        )
      case 'select':
        return (
          <Select
            id={id}
            value={current}
            onChange={(e) => onChange(e.target.value)}
            className="h-7 text-xs"
          >
            <option value="">—</option>
            {(r.options ?? []).map((opt) => (
              <option key={opt} value={opt}>{opt}</option>
            ))}
          </Select>
        )
      default:
        return (
          <Input
            id={id}
            value={current}
            onChange={(e) => onChange(e.target.value)}
            className="h-7 text-xs"
          />
        )
    }
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
            Custom Fields
          </CardTitle>
          {canEdit && !editValues && (
            <button
              className="min-h-6 rounded px-2 py-1 text-xs text-blue-600 hover:underline"
              onClick={() => {
                const init: Record<string, string> = {}
                for (const r of rows) if (r.editable) init[r.id] = r.value
                setEditValues(init)
              }}
            >
              Edit
            </button>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        {rows.map((r) => (
          <div key={r.id} className="space-y-0.5">
            <Label htmlFor={`custom-field-${r.id}`} className="text-xs text-gray-500 wrap-anywhere">
              {r.name}
            </Label>
            {r.inactive && isStaffOrAdmin && (
              <span className="ml-2 rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-500">Inactive</span>
            )}
            {r.editable && editValues ? (
              renderInput(r)
            ) : (
              <p className="text-sm wrap-anywhere">{r.value || <span className="text-gray-400">—</span>}</p>
            )}
          </div>
        ))}
        {canEdit && editValues && (
          <div className="flex gap-2 pt-1">
            <Button
              size="sm"
              className="h-7 text-xs"
              onClick={() => saveMutation.mutate()}
              disabled={saveMutation.isPending}
            >
              {saveMutation.isPending ? 'Saving…' : 'Save'}
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-xs"
              onClick={() => { setEditValues(null); setSaveError('') }}
            >
              Cancel
            </Button>
          </div>
        )}
        {saveError && <p role="alert" className="text-xs text-red-600">{saveError}</p>}
      </CardContent>
    </Card>
  )
}
