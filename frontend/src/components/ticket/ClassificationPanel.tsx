import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listPublicCategories, listPublicTypes, listPublicItems, updateTicket } from '@/api/tickets'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { Label } from '@/components/ui/label'
import type { Category, TicketType, TicketItem } from '@/api/types'

// selectable is the options list for one tier: everything still in
// circulation, plus the row this ticket already carries even if it has since
// been archived.
//
// Two questions were being answered by one filter. "What may staff classify
// NEW work under" is active only, and a test pins that. "May this ticket keep
// the classification it already has" is a different question, and the answer
// has to be yes — otherwise a ticket filed under a category that was retired
// afterwards could be moved OUT of it but never left where it was, and
// editing its Type alone would quietly strip its Category. The server allows
// staff to reclassify into an archived classification for the same reason:
// filing an old ticket where it actually belongs is ordinary work.
function selectable<T extends { id: string; active: boolean }>(rows: T[], current?: string | null): T[] {
  return rows.filter((r) => r.active || r.id === current)
}

export interface ClassificationPanelProps {
  ticketId: string
  categoryId: string
  typeId?: string | null
  itemId?: string | null
  canEdit: boolean
}

/**
 * The Category/Type/Item panel on a ticket.
 *
 * Extracted from TicketDetailPage, which held this alongside the reply
 * composer, the timeline and four lifecycle mutations in one 676-line
 * component. Nothing here is shared with the rest of that page: the three
 * queries, the five pieces of edit state and the mutation are used by this
 * panel and nothing else, which is what made it the first thing to lift out.
 *
 * It owns its own edit state rather than taking it from the parent. CTI is
 * three dependent dropdowns — picking a Category invalidates the chosen Type,
 * and a Type invalidates the Item — and that cascade is only coherent if one
 * component owns all three.
 */
export function ClassificationPanel({ ticketId, categoryId, typeId, itemId, canEdit }: ClassificationPanelProps) {
  const qc = useQueryClient()

  const [editing, setEditing] = useState(false)
  const [draftCategory, setDraftCategory] = useState('')
  const [draftType, setDraftType] = useState('')
  const [draftItem, setDraftItem] = useState('')
  const [error, setError] = useState('')

  const { data: categories = [] } = useQuery<Category[]>({
    queryKey: ['public-categories'],
    queryFn: listPublicCategories,
  })

  // While editing, the lists follow the draft selection; otherwise they follow
  // the ticket, so the display names below resolve.
  const activeCategory = draftCategory || categoryId
  const { data: types = [] } = useQuery<TicketType[]>({
    queryKey: ['public-types', activeCategory],
    queryFn: () => listPublicTypes(activeCategory),
    enabled: !!activeCategory,
  })

  const activeType = draftType || typeId || ''
  const { data: items = [] } = useQuery<TicketItem[]>({
    queryKey: ['public-items', activeCategory, activeType],
    queryFn: () => listPublicItems(activeCategory, activeType),
    enabled: !!activeType,
  })

  const mutation = useMutation({
    mutationFn: () => updateTicket(ticketId, {
      category_id: draftCategory || categoryId,
      // Empty means "cleared", which is null on the wire — not omitted, which
      // would leave the existing value alone.
      type_id: draftType || null,
      item_id: draftItem || null,
    }),
    onSuccess: () => {
      setEditing(false)
      setError('')
      qc.invalidateQueries({ queryKey: ['ticket', ticketId] })
      // Recategorizing changes which canned responses are in scope.
      qc.invalidateQueries({ queryKey: ['ticket-canned-responses', ticketId] })
    },
    onError: (err) => setError(extractError(err)),
  })

  function startEdit() {
    setDraftCategory(categoryId ?? '')
    setDraftType(typeId ?? '')
    setDraftItem(itemId ?? '')
    setError('')
    setEditing(true)
  }

  // Ids are scoped to the ticket so two panels on one page cannot collide.
  const categorySelectId = `cti-category-${ticketId}`
  const typeSelectId = `cti-type-${ticketId}`
  const itemSelectId = `cti-item-${ticketId}`

  // These resolve against the lists above, which for staff now include
  // archived rows. Before, a ticket filed under a category that had since
  // been retired showed "—" in the sidebar: the id was real, the row was
  // simply missing from an active-only list.
  const categoryName = categories.find((c) => c.id === categoryId)?.name
  const typeName = types.find((t) => t.id === typeId)?.name
  const itemName = items.find((i) => i.id === itemId)?.name

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
            Classification
          </CardTitle>
          {canEdit && !editing && (
            <button className="text-xs text-blue-600 hover:underline" onClick={startEdit}>
              Edit
            </button>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        {editing ? (
          <div className="space-y-2">
            <div className="space-y-0.5">
              <Label htmlFor={categorySelectId} className="text-xs text-gray-500">Category</Label>
              <Select
                id={categorySelectId}
                className="h-7 text-xs w-full"
                value={draftCategory}
                onChange={(e) => { setDraftCategory(e.target.value); setDraftType(''); setDraftItem('') }}
              >
                <option value="">— select —</option>
                {selectable(categories, categoryId).map((c) => (
                  <option key={c.id} value={c.id}>{c.name}</option>
                ))}
              </Select>
            </div>
            {types.length > 0 && (
              <div className="space-y-0.5">
                <Label htmlFor={typeSelectId} className="text-xs text-gray-500">Type</Label>
                <Select
                  id={typeSelectId}
                  className="h-7 text-xs w-full"
                  value={draftType}
                  onChange={(e) => { setDraftType(e.target.value); setDraftItem('') }}
                >
                  <option value="">— none —</option>
                  {selectable(types, typeId).map((t) => (
                    <option key={t.id} value={t.id}>{t.name}</option>
                  ))}
                </Select>
              </div>
            )}
            {items.length > 0 && (
              <div className="space-y-0.5">
                <Label htmlFor={itemSelectId} className="text-xs text-gray-500">Item</Label>
                <Select
                  id={itemSelectId}
                  className="h-7 text-xs w-full"
                  value={draftItem}
                  onChange={(e) => setDraftItem(e.target.value)}
                >
                  <option value="">— none —</option>
                  {selectable(items, itemId).map((i) => (
                    <option key={i.id} value={i.id}>{i.name}</option>
                  ))}
                </Select>
              </div>
            )}
            {error && <p role="alert" className="text-xs text-red-600">{error}</p>}
            <div className="flex gap-2 pt-1">
              <Button
                size="sm"
                className="h-7 text-xs"
                onClick={() => mutation.mutate()}
                disabled={mutation.isPending || !draftCategory}
              >
                {mutation.isPending ? 'Saving…' : 'Save'}
              </Button>
              <Button
                size="sm"
                variant="outline"
                className="h-7 text-xs"
                onClick={() => { setEditing(false); setError('') }}
              >
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <>
            <div className="flex justify-between">
              <span className="text-gray-500">Category</span>
              <span className="text-right text-xs font-medium">{categoryName ?? '—'}</span>
            </div>
            {typeId && (
              <div className="flex justify-between">
                <span className="text-gray-500">Type</span>
                <span className="text-right text-xs">{typeName ?? '—'}</span>
              </div>
            )}
            {itemId && (
              <div className="flex justify-between">
                <span className="text-gray-500">Item</span>
                <span className="text-right text-xs">{itemName ?? '—'}</span>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
