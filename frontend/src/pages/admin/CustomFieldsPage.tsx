import { useEffect, useId, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useSearch } from '@tanstack/react-router'
import {
  listFieldDefs, createFieldDef, updateFieldDef, reorderFieldDefs, listFieldPlacements, getFieldUsage,
  listCategories, listTypes, listItems,
  createCategoryAssignment, createTypeAssignment, createItemAssignment,
  updateCategoryAssignment, updateTypeAssignment, updateItemAssignment,
  deleteCategoryAssignment, deleteTypeAssignment, deleteItemAssignment,
} from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { SortableList } from '@/components/admin/SortableList'
import { TOUCH_SIZE } from '@/components/admin/touchTargets'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { ChevronDownIcon, ChevronRightIcon, PlusIcon, XIcon } from 'lucide-react'
import type { FieldDef, FieldPlacement, FieldType, FieldUsage } from '@/api/types'

const FIELD_TYPES: { value: FieldType; label: string }[] = [
  { value: 'text', label: 'Text' },
  { value: 'textarea', label: 'Text area' },
  { value: 'number', label: 'Number' },
  { value: 'select', label: 'Select' },
]

const typeLabel = (t: FieldType) => FIELD_TYPES.find((x) => x.value === t)?.label ?? t

// Every call a field's row makes lives under the list's key, so one invalidate of
// ['admin', 'custom-fields', id] refreshes that row's places and counts together.
const FIELDS_KEY = ['admin', 'custom-fields'] as const
const placesKey = (id: string) => [...FIELDS_KEY, id, 'assignments'] as const
const usageKey = (id: string) => [...FIELDS_KEY, id, 'usage'] as const

// What the Categories page, the new-ticket form and the ticket card cached about
// a field or its places: the first holds one level's attachments, the second a
// classification's resolved fields (cached for 30 seconds, so a stale list would
// outlive the change). Anything that changes either must clear both.
function invalidateForms(qc: QueryClient) {
  qc.invalidateQueries({ queryKey: ['ctiFields'] })
  qc.invalidateQueries({ queryKey: ['ctiFieldsResolved'] })
}

// Small buttons are h-8; a finger needs 44px.
const BTN_TOUCH = 'pointer-coarse:min-h-11 pointer-coarse:min-w-11'
const CHECK_LABEL = `inline-flex cursor-pointer select-none items-center gap-2 text-sm text-gray-700 ${TOUCH_SIZE}`
const LABEL = 'block text-xs font-medium uppercase tracking-wide text-gray-500'
const SECTION_HEAD = 'text-xs font-semibold uppercase tracking-wide text-gray-500'

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

// ── Options ───────────────────────────────────────────────────────────────────

// An option in use. `original` is the text it had when the page loaded, or null
// for one typed in since: the server stores options as plain text, so the
// original is how a rename is told apart from a new option.
interface OptionRow { key: string; original: string | null; text: string }
interface RetiredRow { key: string; text: string }

function useOptionsDraft(options: string[], retired: string[]) {
  const seq = useRef(0)
  const initialRows = () => options.map((o): OptionRow => ({ key: `o:${o}`, original: o, text: o }))
  const initialRetired = () => retired.map((o): RetiredRow => ({ key: `r:${o}`, text: o }))
  const [rows, setRows] = useState<OptionRow[]>(initialRows)
  const [retiredRows, setRetiredRows] = useState<RetiredRow[]>(initialRetired)

  return {
    rows,
    retiredRows,
    setText: (key: string, text: string) => setRows((rs) => rs.map((r) => (r.key === key ? { ...r, text } : r))),
    add: () => setRows((rs) => [...rs, { key: `new-${seq.current++}`, original: null, text: '' }]),
    remove: (key: string) => setRows((rs) => rs.filter((r) => r.key !== key)),
    move: (keys: string[]) => setRows((rs) => keys.flatMap((k) => rs.find((r) => r.key === k) ?? [])),
    // Retiring acts on the text the option was loaded with: an edit typed since
    // is not what tickets hold.
    retire: (key: string) => {
      const row = rows.find((r) => r.key === key)
      if (!row || row.original === null) return
      setRows((rs) => rs.filter((r) => r.key !== key))
      setRetiredRows((rr) => [...rr, { key: `r:${row.original}`, text: row.original as string }])
    },
    bringBack: (key: string) => {
      const row = retiredRows.find((r) => r.key === key)
      if (!row) return
      setRetiredRows((rr) => rr.filter((r) => r.key !== key))
      setRows((rs) => [...rs, { key: `o:${row.text}`, original: row.text, text: row.text }])
    },
    removeRetired: (key: string) => setRetiredRows((rr) => rr.filter((r) => r.key !== key)),
    reset: () => { setRows(initialRows()); setRetiredRows(initialRetired()) },
  }
}
type OptionsDraft = ReturnType<typeof useOptionsDraft>

function ticketsHolding(usage: FieldUsage | undefined, value: string): number | undefined {
  return usage?.values.find((v) => v.value === value)?.tickets
}

// A rename of an option tickets hold keeps the old text, retired, so those
// tickets still show it; renaming an unused one edits it in place. Not knowing
// the count counts as held: retiring is the safe side.
function isRenameOfHeld(row: OptionRow, usage: FieldUsage | undefined): boolean {
  if (row.original === null || row.text.trim() === row.original) return false
  const n = ticketsHolding(usage, row.original)
  return n === undefined || n > 0
}

function optionLists(draft: OptionsDraft, usage: FieldUsage | undefined) {
  const options = draft.rows.map((r) => r.text.trim())
  const retired = draft.retiredRows.map((r) => r.text)
  for (const r of draft.rows) {
    if (isRenameOfHeld(r, usage)) retired.push(r.original as string)
  }
  return { options, retired }
}

// Blank and duplicate options are refused here, not by the server. Blank is held
// back until the first Save so that adding a row does not shout at once.
function optionErrors(draft: OptionsDraft, retired: string[], attempted: boolean) {
  const byKey: Record<string, string> = {}
  const seen = new Set(retired)
  for (const r of draft.rows) {
    const t = r.text.trim()
    if (!t) {
      if (attempted) byKey[r.key] = "An option can't be blank."
    } else if (seen.has(t)) {
      byKey[r.key] = `"${t}" is already an option.`
    } else {
      seen.add(t)
    }
  }
  return byKey
}

const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((v, i) => v === b[i])

function OptionsEditor({
  draft, usage, attempted, persisted, showRetired = false, onShowRetired = () => {},
}: {
  draft: OptionsDraft
  // Undefined while loading, or when the field is not created yet.
  usage: FieldUsage | undefined
  attempted: boolean
  // False on the create form, where nothing is stored yet: no counts, no retiring.
  persisted: boolean
  // Held by the row, not here: a Save rebuilds the form, and the retired list
  // should not fold away under the option that was just retired.
  showRetired?: boolean
  onShowRetired?: (show: boolean) => void
}) {
  const { retired } = optionLists(draft, usage)
  const errors = optionErrors(draft, retired, attempted)

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-x-4">
        <span className={LABEL}>Options</span>
        {persisted && (
          <label className={CHECK_LABEL}>
            <input type="checkbox" checked={showRetired} onChange={(e) => onShowRetired(e.target.checked)} />
            Show retired
          </label>
        )}
      </div>

      <SortableList
        items={draft.rows.map((r) => ({ id: r.key, label: r.text.trim() || 'new option', row: r }))}
        onReorder={draft.move}
        renderItem={({ row }, handle) => {
          const i = draft.rows.indexOf(row)
          const n = row.original === null ? undefined : ticketsHolding(usage, row.original)
          const name = row.text.trim() || 'new option'
          return (
            <div className="py-1">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                {handle}
                <Input
                  aria-label={`Option ${i + 1}`}
                  value={row.text}
                  onChange={(e) => draft.setText(row.key, e.target.value)}
                  className="h-9 min-w-0 flex-1 basis-40 pointer-coarse:h-11"
                />
                <div className="flex items-center gap-2 max-sm:basis-full max-sm:justify-end">
                  {n !== undefined && <span className="whitespace-nowrap text-xs text-gray-500">{plural(n, 'ticket', 'tickets')}</span>}
                  {row.original !== null && (
                    <Button type="button" size="sm" variant="outline" className={BTN_TOUCH} aria-label={`Retire ${name}`} onClick={() => draft.retire(row.key)}>
                      Retire
                    </Button>
                  )}
                  {row.original === null ? (
                    <Button type="button" size="sm" variant="ghost" className={BTN_TOUCH} aria-label={`Remove ${name}`} onClick={() => draft.remove(row.key)}>
                      Remove
                    </Button>
                  ) : n === 0 ? (
                    <Button type="button" size="sm" variant="outline" className={`text-red-700 ${BTN_TOUCH}`} aria-label={`Delete ${name}`} onClick={() => draft.remove(row.key)}>
                      Delete
                    </Button>
                  ) : null}
                </div>
              </div>
              {errors[row.key] && <p role="alert" className="mt-1 text-xs text-red-600 wrap-anywhere">{errors[row.key]}</p>}
              {isRenameOfHeld(row, usage) && ticketsHolding(usage, row.original as string) !== undefined && (
                <p className="mt-1 text-xs text-gray-500 wrap-anywhere">
                  {plural(ticketsHolding(usage, row.original as string) as number, 'ticket keeps', 'tickets keep')} "{row.original}" as a retired option.
                </p>
              )}
            </div>
          )
        }}
      />

      {draft.rows.length === 0 && <p role="alert" className="text-xs text-red-600">Add at least one option.</p>}
      <Button type="button" size="sm" variant="outline" className={BTN_TOUCH} onClick={draft.add}>
        <PlusIcon aria-hidden="true" className="mr-1 h-3.5 w-3.5" /> Add option
      </Button>

      {persisted && showRetired && (
        <div className="space-y-1 border-t pt-2">
          <span className={LABEL}>Retired</span>
          {draft.retiredRows.length === 0 && <p className="text-sm text-gray-500">No retired options.</p>}
          {draft.retiredRows.map((r) => {
            const n = ticketsHolding(usage, r.text)
            return (
              <div key={r.key} className="flex flex-wrap items-center gap-x-2 gap-y-1 py-1">
                <span className="min-w-0 flex-1 basis-40 text-sm text-gray-700 wrap-anywhere">{r.text}</span>
                <div className="flex items-center gap-2 max-sm:basis-full max-sm:justify-end">
                  {n !== undefined && <span className="whitespace-nowrap text-xs text-gray-500">{plural(n, 'ticket', 'tickets')}</span>}
                  <Button type="button" size="sm" variant="outline" className={BTN_TOUCH} aria-label={`Bring back ${r.text}`} onClick={() => draft.bringBack(r.key)}>
                    Bring back
                  </Button>
                  {n === 0 && (
                    <Button type="button" size="sm" variant="outline" className={`text-red-700 ${BTN_TOUCH}`} aria-label={`Delete ${r.text}`} onClick={() => draft.removeRetired(r.key)}>
                      Delete
                    </Button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ── Details ───────────────────────────────────────────────────────────────────

function FieldDetails({ def, showRetired, onShowRetired }: { def: FieldDef; showRetired: boolean; onShowRetired: (show: boolean) => void }) {
  const qc = useQueryClient()
  const ids = useId()
  const [name, setName] = useState(def.name)
  const [type, setType] = useState<FieldType>(def.field_type)
  const draft = useOptionsDraft(def.options ?? [], def.retired_options ?? [])
  const [attempted, setAttempted] = useState(false)
  const [error, setError] = useState('')

  const usageQuery = useQuery({ queryKey: usageKey(def.id), queryFn: () => getFieldUsage(def.id) })
  const usage = usageQuery.data
  const inUse = (usage?.tickets_with_value ?? 0) > 0

  const { options, retired } = optionLists(draft, usage)

  // Only what changed goes in the PATCH: a field that was not touched is not
  // rewritten, and the server then has nothing of ours to refuse.
  const patch: Parameters<typeof updateFieldDef>[1] = {}
  if (name.trim() !== def.name) patch.name = name.trim()
  if (type !== def.field_type) patch.field_type = type
  if (type === 'select') {
    if (!sameList(options, def.options ?? [])) patch.options = options
    if (!sameList(retired, def.retired_options ?? [])) patch.retired_options = retired
  }
  const dirty = Object.keys(patch).length > 0

  const saveMutation = useMutation({
    mutationFn: () => updateFieldDef(def.id, patch),
    onSuccess: () => {
      setError('')
      setAttempted(false)
      qc.invalidateQueries({ queryKey: FIELDS_KEY })
      invalidateForms(qc)
    },
    onError: (err) => {
      setError(extractError(err))
      // The refusal usually means a count was out of date.
      qc.invalidateQueries({ queryKey: usageKey(def.id) })
    },
  })

  function save() {
    setAttempted(true)
    if (!name.trim()) return
    if (type === 'select') {
      if (draft.rows.length === 0) return
      if (Object.keys(optionErrors(draft, retired, true)).length > 0) return
    }
    saveMutation.mutate()
  }

  function cancel() {
    setName(def.name)
    setType(def.field_type)
    draft.reset()
    setAttempted(false)
    setError('')
  }

  function changeType(next: FieldType) {
    setType(next)
    // A select needs somewhere to type its first option.
    if (next === 'select' && draft.rows.length === 0) draft.add()
  }

  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <label htmlFor={`${ids}-name`} className={LABEL}>Name</label>
          <Input id={`${ids}-name`} value={name} onChange={(e) => setName(e.target.value)} className="pointer-coarse:h-11" />
        </div>
        {inUse ? (
          <div className="space-y-1">
            <span className={LABEL}>Type</span>
            <p className="text-sm font-medium text-gray-900">{typeLabel(def.field_type)}</p>
            <p className="text-xs text-gray-500">
              Fixed: {plural(usage?.tickets_with_value ?? 0, 'ticket has', 'tickets have')} a value for this field.
            </p>
          </div>
        ) : (
          <div className="space-y-1">
            <label htmlFor={`${ids}-type`} className={LABEL}>Type</label>
            <Select
              id={`${ids}-type`}
              value={type}
              // Until the count arrives the type could still be locked.
              disabled={!usageQuery.isSuccess}
              onChange={(e) => changeType(e.target.value as FieldType)}
              className="pointer-coarse:h-11"
            >
              {FIELD_TYPES.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
            </Select>
            {usageQuery.isError && <p className="text-xs text-gray-500">Could not check whether tickets use this field.</p>}
          </div>
        )}
      </div>

      {type === 'select' && <OptionsEditor draft={draft} usage={usage} attempted={attempted} persisted showRetired={showRetired} onShowRetired={onShowRetired} />}

      {attempted && !name.trim() && <p role="alert" className="text-sm text-red-600">A field needs a name.</p>}
      {error && <p role="alert" className="text-sm text-red-600 wrap-anywhere">{error}</p>}
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="outline" className={BTN_TOUCH} onClick={cancel} disabled={saveMutation.isPending}>Cancel</Button>
        <Button size="sm" className={BTN_TOUCH} onClick={save} disabled={!dirty || saveMutation.isPending}>
          {saveMutation.isPending ? 'Saving…' : 'Save'}
        </Button>
      </div>
    </div>
  )
}

// ── Where it appears ──────────────────────────────────────────────────────────

interface Scope { categoryId: string; typeId?: string; itemId?: string }
interface PlaceInput { field_def_id: string; visible_on_new: boolean; required_on_new: boolean }
interface PlacePatch { visible_on_new?: boolean; required_on_new?: boolean }

const scopeOf = (p: FieldPlacement): Scope => ({ categoryId: p.category.id, typeId: p.type?.id, itemId: p.item?.id })

// The three levels have three sets of routes; the scope says which to use.
function createPlace(s: Scope, input: PlaceInput) {
  if (s.itemId && s.typeId) return createItemAssignment(s.categoryId, s.typeId, s.itemId, input)
  if (s.typeId) return createTypeAssignment(s.categoryId, s.typeId, input)
  return createCategoryAssignment(s.categoryId, input)
}
function updatePlace(s: Scope, id: string, patch: PlacePatch) {
  if (s.itemId && s.typeId) return updateItemAssignment(s.categoryId, s.typeId, s.itemId, id, patch)
  if (s.typeId) return updateTypeAssignment(s.categoryId, s.typeId, id, patch)
  return updateCategoryAssignment(s.categoryId, id, patch)
}
function deletePlace(s: Scope, id: string) {
  if (s.itemId && s.typeId) return deleteItemAssignment(s.categoryId, s.typeId, s.itemId, id)
  if (s.typeId) return deleteTypeAssignment(s.categoryId, s.typeId, id)
  return deleteCategoryAssignment(s.categoryId, id)
}

const pathOf = (p: FieldPlacement) => [p.category.name, p.type?.name, p.item?.name].filter(Boolean).join(' › ')
const isArchived = (p: FieldPlacement) => !p.category.active || p.type?.active === false || p.item?.active === false

// What the places add up to, in the words an administrator would use.
function describePlaces(places: FieldPlacement[]): string {
  const shown = places.filter((p) => p.visible_on_new)
  if (shown.length === 0) return 'Not shown on the new-ticket form.'
  const parts = shown.map((p) => {
    const where = p.scope_type === 'category' ? `${pathOf(p)} (any type)` : pathOf(p)
    return p.required_on_new ? `${where}, required` : where
  })
  return `Shown when a ticket is filed under ${parts.join(', or under ')}.`
}

// Which place the settings of an overlapping pair come from: the more specific
// one wins. The API allows an overlap; this page does not create one, but lists
// any that exist.
function overlapsBelow(p: FieldPlacement, all: FieldPlacement[]): FieldPlacement[] {
  return all.filter((q) => {
    if (q.id === p.id) return false
    if (p.scope_type === 'category') return q.scope_type !== 'category' && q.category.id === p.category.id
    if (p.scope_type === 'type') return q.scope_type === 'item' && q.type?.id === p.type?.id
    return false
  })
}

function PlacesSection({ def, autoFocusAdd }: { def: FieldDef; autoFocusAdd: boolean }) {
  const qc = useQueryClient()
  const addRef = useRef<HTMLButtonElement>(null)
  const [focusOnMount] = useState(autoFocusAdd)
  const [formOpen, setFormOpen] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (focusOnMount) addRef.current?.focus()
  }, [focusOnMount])

  const placesQuery = useQuery({ queryKey: placesKey(def.id), queryFn: () => listFieldPlacements(def.id) })
  const places = placesQuery.data ?? []

  function refresh() {
    qc.invalidateQueries({ queryKey: [...FIELDS_KEY, def.id] })
    // The list's place_count for this row.
    qc.invalidateQueries({ queryKey: FIELDS_KEY, exact: true })
    invalidateForms(qc)
  }

  const toggleMutation = useMutation({
    mutationFn: ({ p, patch }: { p: FieldPlacement; patch: PlacePatch }) => updatePlace(scopeOf(p), p.id, patch),
    onSuccess: () => { setError(''); refresh() },
    onError: (err) => setError(extractError(err)),
  })
  const removeMutation = useMutation({
    mutationFn: (p: FieldPlacement) => deletePlace(scopeOf(p), p.id),
    onSuccess: () => { setError(''); refresh() },
    onError: (err) => setError(extractError(err)),
  })

  const showForm = formOpen || (placesQuery.isSuccess && places.length === 0)

  return (
    <div className="space-y-3">
      {placesQuery.isLoading && <Spinner />}
      {placesQuery.isError && <p role="alert" className="text-sm text-red-600">Could not load where this field appears.</p>}
      {placesQuery.isSuccess && places.length === 0 && <p className="text-sm text-gray-600">Not on any form yet.</p>}

      {places.length > 0 && (
        <>
          <ul className="divide-y rounded-md border bg-white">
            {places.map((p) => {
              const path = pathOf(p)
              const below = overlapsBelow(p, places)
              return (
                <li key={p.id} className="px-3 py-2">
                  <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
                    <span className="min-w-0 flex-1 basis-40 text-sm font-medium text-gray-800 wrap-anywhere max-sm:order-1">
                      {path}
                      {isArchived(p) && <Badge variant="secondary" className="ml-2">Archived</Badge>}
                    </span>
                    <label className={`${CHECK_LABEL} max-sm:order-3 max-sm:basis-full pointer-coarse:justify-start`}>
                      {/* The place goes in the name so a screen reader can tell the rows apart. */}
                      <input
                        type="checkbox"
                        aria-label={`Show on new-ticket form at ${path}`}
                        checked={p.visible_on_new}
                        onChange={(e) => toggleMutation.mutate({ p, patch: { visible_on_new: e.target.checked } })}
                      />
                      Show on new-ticket form
                    </label>
                    <label className={`${CHECK_LABEL} max-sm:order-4 max-sm:basis-full pointer-coarse:justify-start`}>
                      <input
                        type="checkbox"
                        aria-label={`Required at ${path}`}
                        checked={p.required_on_new}
                        // Required only means anything on a form the field is shown on.
                        disabled={!p.visible_on_new}
                        onChange={(e) => toggleMutation.mutate({ p, patch: { required_on_new: e.target.checked } })}
                      />
                      Required
                    </label>
                    <button
                      type="button"
                      aria-label={`Remove ${path}`}
                      onClick={() => removeMutation.mutate(p)}
                      disabled={removeMutation.isPending}
                      className={`rounded text-gray-400 hover:text-red-600 max-sm:order-2 ${TOUCH_SIZE}`}
                    >
                      <XIcon aria-hidden="true" className="h-4 w-4" />
                    </button>
                  </div>
                  {below.length > 0 && (
                    <p className="mt-1 text-xs text-amber-700 wrap-anywhere">
                      Overlaps {below.map(pathOf).join(', ')}, where that place's settings apply.
                    </p>
                  )}
                </li>
              )
            })}
          </ul>
          <p className="text-sm text-gray-600 wrap-anywhere">{describePlaces(places)}</p>
          {places.some((p) => p.scope_type === 'item' && p.visible_on_new) && (
            <p className="text-sm text-gray-600">Requesters don't pick items, so only staff will see this.</p>
          )}
        </>
      )}
      {error && <p role="alert" className="text-sm text-red-600 wrap-anywhere">{error}</p>}

      <Button
        ref={addRef}
        type="button"
        size="sm"
        variant="outline"
        className={BTN_TOUCH}
        aria-expanded={showForm}
        onClick={() => setFormOpen(true)}
      >
        <PlusIcon aria-hidden="true" className="mr-1 h-3.5 w-3.5" /> Add a place
      </Button>

      {showForm && (
        <AddPlaceForm
          def={def}
          places={places}
          onAdded={refresh}
          onCancel={places.length > 0 ? () => setFormOpen(false) : undefined}
        />
      )}
    </div>
  )
}

interface Candidate { categoryId: string; typeId: string; itemId: string }

// Why a place cannot be added, or null. An existing place that covers the
// candidate, or is the candidate, makes it pointless; one below it would overlap.
function conflictFor(c: Candidate, places: FieldPlacement[]): string | null {
  for (const p of places) {
    if (c.itemId) {
      if (p.item?.id === c.itemId || (p.scope_type === 'type' && p.type?.id === c.typeId) || (p.scope_type === 'category' && p.category.id === c.categoryId)) {
        return 'already attached'
      }
    } else if (c.typeId) {
      if ((p.scope_type === 'type' && p.type?.id === c.typeId) || (p.scope_type === 'category' && p.category.id === c.categoryId)) {
        return 'already attached'
      }
      if (p.scope_type === 'item' && p.type?.id === c.typeId) return `overlaps ${pathOf(p)}`
    } else {
      if (p.scope_type === 'category' && p.category.id === c.categoryId) return 'already attached'
      if (p.scope_type !== 'category' && p.category.id === c.categoryId) return `overlaps ${pathOf(p)}`
    }
  }
  return null
}

function AddPlaceForm({
  def, places, onAdded, onCancel,
}: {
  def: FieldDef
  places: FieldPlacement[]
  onAdded: () => void
  onCancel?: () => void
}) {
  const ids = useId()
  const [cand, setCand] = useState<Candidate>({ categoryId: '', typeId: '', itemId: '' })
  const [show, setShow] = useState(true)
  const [required, setRequired] = useState(false)

  const { data: categories = [] } = useQuery({ queryKey: ['admin', 'categories'], queryFn: listCategories })
  const { data: types = [] } = useQuery({
    queryKey: ['admin', 'types', cand.categoryId],
    queryFn: () => listTypes(cand.categoryId),
    enabled: !!cand.categoryId,
  })
  const { data: items = [] } = useQuery({
    queryKey: ['admin', 'items', cand.categoryId, cand.typeId],
    queryFn: () => listItems(cand.categoryId, cand.typeId),
    enabled: !!cand.categoryId && !!cand.typeId,
  })

  const addMutation = useMutation({
    mutationFn: () =>
      createPlace(
        { categoryId: cand.categoryId, typeId: cand.typeId || undefined, itemId: cand.itemId || undefined },
        { field_def_id: def.id, visible_on_new: show, required_on_new: required && show },
      ),
    onSuccess: () => {
      setCand({ categoryId: '', typeId: '', itemId: '' })
      setShow(true)
      setRequired(false)
      onAdded()
    },
  })

  const conflict = cand.categoryId ? conflictFor(cand, places) : null
  const suffix = (reason: string | null) => (reason ? ` — ${reason}` : '')
  const archived = (active: boolean) => (active ? '' : ' (archived)')

  return (
    <div role="group" aria-label="Add a place" className="space-y-3 rounded-md border bg-white p-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <label htmlFor={`${ids}-cat`} className={LABEL}>Category</label>
          <Select
            id={`${ids}-cat`}
            value={cand.categoryId}
            onChange={(e) => setCand({ categoryId: e.target.value, typeId: '', itemId: '' })}
            className="pointer-coarse:h-11"
          >
            <option value="">Choose a category…</option>
            {categories.map((c) => {
              const taken = places.some((p) => p.scope_type === 'category' && p.category.id === c.id)
              return (
                <option key={c.id} value={c.id} disabled={taken}>
                  {c.name}{archived(c.active)}{suffix(taken ? 'already attached' : null)}
                </option>
              )
            })}
          </Select>
        </div>
        {cand.categoryId && (
          <div className="space-y-1">
            <label htmlFor={`${ids}-type`} className={LABEL}>Type</label>
            <Select
              id={`${ids}-type`}
              value={cand.typeId}
              onChange={(e) => setCand({ ...cand, typeId: e.target.value, itemId: '' })}
              className="pointer-coarse:h-11"
            >
              {(() => {
                const why = conflictFor({ ...cand, typeId: '', itemId: '' }, places)
                return <option value="" disabled={!!why}>Whole category{suffix(why)}</option>
              })()}
              {types.map((t) => {
                const why = places.some((p) => (p.scope_type === 'type' && p.type?.id === t.id) || (p.scope_type === 'category' && p.category.id === cand.categoryId))
                  ? 'already attached' : null
                return (
                  <option key={t.id} value={t.id} disabled={!!why}>{t.name}{archived(t.active)}{suffix(why)}</option>
                )
              })}
            </Select>
          </div>
        )}
        {cand.categoryId && cand.typeId && (
          <div className="space-y-1">
            <label htmlFor={`${ids}-item`} className={LABEL}>Item</label>
            <Select
              id={`${ids}-item`}
              value={cand.itemId}
              onChange={(e) => setCand({ ...cand, itemId: e.target.value })}
              className="pointer-coarse:h-11"
            >
              {(() => {
                const why = conflictFor({ ...cand, itemId: '' }, places)
                return <option value="" disabled={!!why}>Whole type{suffix(why)}</option>
              })()}
              {items.map((i) => {
                const why = places.some((p) => p.item?.id === i.id) ? 'already attached' : null
                return (
                  <option key={i.id} value={i.id} disabled={!!why}>{i.name}{archived(i.active)}{suffix(why)}</option>
                )
              })}
            </Select>
          </div>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <label className={CHECK_LABEL}>
          <input type="checkbox" checked={show} onChange={(e) => setShow(e.target.checked)} />
          Show on new-ticket form
        </label>
        <label className={CHECK_LABEL}>
          <input type="checkbox" checked={required} disabled={!show} onChange={(e) => setRequired(e.target.checked)} />
          Required
        </label>
      </div>

      {conflict && <p className="text-xs text-amber-700 wrap-anywhere">That place is {conflict}. Choose another.</p>}
      {addMutation.isError && <p role="alert" className="text-sm text-red-600">{extractError(addMutation.error)}</p>}
      <div className="flex gap-2">
        <Button size="sm" className={BTN_TOUCH} onClick={() => addMutation.mutate()} disabled={!cand.categoryId || !!conflict || addMutation.isPending}>
          Add
        </Button>
        {onCancel && <Button size="sm" variant="outline" className={BTN_TOUCH} onClick={onCancel}>Cancel</Button>}
      </div>
    </div>
  )
}

// ── A row ─────────────────────────────────────────────────────────────────────

function FieldRow({
  def, handle, expanded, onToggle, autoFocusAdd, alignWithInactive,
}: {
  def: FieldDef
  handle: React.ReactNode
  expanded: boolean
  onToggle: () => void
  autoFocusAdd: boolean
  // Inactive rows carry a badge and a button on the right; an active row leaves
  // the same room, so the type and place columns line up down the list.
  alignWithInactive: boolean
}) {
  const qc = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [showRetired, setShowRetired] = useState(false)

  // The list carries place_count, so a closed row needs no call of its own.
  const count = def.place_count

  const activeMutation = useMutation({
    mutationFn: (active: boolean) => updateFieldDef(def.id, { active }),
    onSuccess: () => {
      setConfirmOpen(false)
      qc.invalidateQueries({ queryKey: FIELDS_KEY })
      invalidateForms(qc)
    },
  })

  // Rebuild the form when the stored field changes under it (after a Save, or
  // someone else's edit) rather than patching its state in an effect.
  const signature = JSON.stringify([def.name, def.field_type, def.options, def.retired_options])

  return (
    <div role="group" aria-label={def.name} id={`cf-${def.id}`} className="border-b last:border-b-0">
      <div className="flex flex-wrap items-center gap-x-1 px-1 py-1">
        {handle}
        <button
          type="button"
          aria-expanded={expanded}
          onClick={onToggle}
          className={`flex min-w-0 flex-1 basis-48 items-center gap-2 rounded py-1 text-left hover:bg-gray-50 ${TOUCH_SIZE} pointer-coarse:justify-start`}
        >
          {expanded
            ? <ChevronDownIcon aria-hidden="true" className="h-4 w-4 shrink-0 text-gray-400" />
            : <ChevronRightIcon aria-hidden="true" className="h-4 w-4 shrink-0 text-gray-400" />}
          <span className="flex min-w-0 flex-1 flex-col sm:flex-row sm:items-center sm:gap-4">
            <span className={`min-w-0 text-sm font-medium wrap-anywhere sm:flex-1 ${def.active ? 'text-gray-900' : 'text-gray-500'}`}>{def.name}</span>
            <span className="flex flex-wrap items-center gap-x-1 text-xs text-gray-500 sm:gap-x-4">
              <span className="sm:w-20">{typeLabel(def.field_type)}</span>
              <span aria-hidden="true" className="sm:hidden">·</span>
              {count === undefined ? null : count === 0
                ? <span className="font-medium text-amber-700 sm:w-44">Not on any form yet</span>
                : <span className="sm:w-44">On {plural(count, 'place', 'places')}</span>}
            </span>
          </span>
        </button>
        {def.active && alignWithInactive && <span aria-hidden="true" className="hidden sm:block sm:w-40" />}
        {!def.active && (
          <span className="flex items-center gap-2 max-sm:basis-full max-sm:justify-end max-sm:pb-1 sm:w-40 sm:justify-end">
            <Badge variant="secondary">Inactive</Badge>
            <Button
              size="sm"
              variant="outline"
              className={`text-green-700 ${BTN_TOUCH}`}
              aria-label={`Reactivate ${def.name}`}
              onClick={() => activeMutation.mutate(true)}
              disabled={activeMutation.isPending}
            >
              Reactivate
            </Button>
          </span>
        )}
      </div>

      {expanded && (
        <div className="space-y-5 border-t bg-gray-50 px-3 py-4 sm:ml-12">
          <section className="space-y-2">
            <h3 className={SECTION_HEAD}>Details</h3>
            <FieldDetails key={signature} def={def} showRetired={showRetired} onShowRetired={setShowRetired} />
          </section>
          <section className="space-y-2 border-t pt-4">
            <h3 className={SECTION_HEAD}>Where it appears</h3>
            <PlacesSection def={def} autoFocusAdd={autoFocusAdd} />
          </section>
          {def.active && (
            <div className="flex justify-end border-t pt-4">
              <Button
                size="sm"
                variant="outline"
                className={`border-yellow-300 text-yellow-700 hover:bg-yellow-50 ${BTN_TOUCH}`}
                onClick={() => setConfirmOpen(true)}
              >
                Deactivate…
              </Button>
            </div>
          )}
        </div>
      )}

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={(open) => { setConfirmOpen(open); if (!open) activeMutation.reset() }}
        error={activeMutation.isError ? extractError(activeMutation.error) : undefined}
        title={`Deactivate ${def.name}?`}
        description="It stops appearing on new tickets and can no longer be changed on any ticket. Tickets that already have a value keep showing it. It will be hidden from this list. Turn on Show inactive to reactivate it."
        confirmLabel="Deactivate"
        destructive={false}
        isPending={activeMutation.isPending}
        onConfirm={() => activeMutation.mutate(false)}
      />
    </div>
  )
}

// ── Create ────────────────────────────────────────────────────────────────────

function CreateForm({
  defs, onCreated, onCancel,
}: {
  defs: FieldDef[]
  onCreated: (created: FieldDef) => Promise<void>
  onCancel: () => void
}) {
  const ids = useId()
  const [name, setName] = useState('')
  const [type, setType] = useState<FieldType>('text')
  const draft = useOptionsDraft([], [])
  const [attempted, setAttempted] = useState(false)

  const options = draft.rows.map((r) => r.text.trim())
  const optionsOk = type !== 'select' || (options.length > 0 && Object.keys(optionErrors(draft, [], true)).length === 0)

  const createMutation = useMutation({
    // Highest number plus one: the new field goes last, and dragging sets the rest.
    mutationFn: () => createFieldDef({
      name: name.trim(),
      field_type: type,
      ...(type === 'select' ? { options } : {}),
      sort_order: defs.reduce((max, d) => Math.max(max, d.sort_order), 0) + 1,
    }),
    onSuccess: onCreated,
  })

  function submit() {
    setAttempted(true)
    if (!name.trim() || !optionsOk) return
    createMutation.mutate()
  }

  return (
    <div
      className="space-y-3 rounded-lg border bg-white p-4"
      onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}
    >
      <p className="text-sm font-medium text-gray-700">New custom field</p>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <label htmlFor={`${ids}-name`} className={LABEL}>Name</label>
          <Input
            id={`${ids}-name`}
            autoFocus
            placeholder="e.g. Asset Tag"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="pointer-coarse:h-11"
          />
        </div>
        <div className="space-y-1">
          <label htmlFor={`${ids}-type`} className={LABEL}>Type</label>
          <Select
            id={`${ids}-type`}
            value={type}
            onChange={(e) => {
              const next = e.target.value as FieldType
              setType(next)
              if (next === 'select' && draft.rows.length === 0) draft.add()
            }}
            className="pointer-coarse:h-11"
          >
            {FIELD_TYPES.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
          </Select>
        </div>
      </div>
      {type === 'select' && <OptionsEditor draft={draft} usage={undefined} attempted={attempted} persisted={false} />}
      {createMutation.isError && <p role="alert" className="text-sm text-red-600">{extractError(createMutation.error)}</p>}
      <div className="flex gap-2">
        <Button className={BTN_TOUCH} onClick={submit} disabled={!name.trim() || createMutation.isPending}>
          {createMutation.isPending ? 'Creating…' : 'Create field'}
        </Button>
        <Button variant="outline" className={BTN_TOUCH} onClick={onCancel}>Cancel</Button>
      </div>
    </div>
  )
}

// Says the field it just made is on no form. It goes away once a place exists,
// since by then it is no longer true.
function CreatedBanner({ name, placeCount, onDismiss }: { name: string; placeCount: number | undefined; onDismiss: () => void }) {
  if (placeCount) return null
  return (
    <div role="status" className="flex items-start justify-between gap-3 rounded-md border border-green-200 bg-green-50 px-3 py-2 text-sm text-green-900">
      <p className="min-w-0 wrap-anywhere">
        {name} created. It isn't on any ticket form yet. Choose where it appears.
      </p>
      <button
        type="button"
        aria-label="Dismiss"
        onClick={onDismiss}
        className={`shrink-0 rounded text-green-800 hover:text-green-950 ${TOUCH_SIZE}`}
      >
        <XIcon aria-hidden="true" className="h-4 w-4" />
      </button>
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

// The new order of every field when only the visible ones were dragged: hidden
// (inactive) fields keep their slot, and the visible ones fill the rest.
function mergeOrder(all: FieldDef[], isVisible: (d: FieldDef) => boolean, visibleIds: string[]): string[] {
  const queue = [...visibleIds]
  return all.map((d) => (isVisible(d) ? (queue.shift() as string) : d.id))
}

export function CustomFieldsPage() {
  const qc = useQueryClient()
  const { field: linkedField } = useSearch({ from: '/admin/custom-fields' })
  const [adding, setAdding] = useState(false)
  const [showInactive, setShowInactive] = useState(false)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(linkedField ? [linkedField] : []))
  const [created, setCreated] = useState<{ id: string; name: string } | null>(null)
  const [focusPlaceFor, setFocusPlaceFor] = useState<string | null>(null)
  const [reorderError, setReorderError] = useState('')

  const { data: defs = [], isLoading, isError } = useQuery({ queryKey: FIELDS_KEY, queryFn: listFieldDefs })

  // A link to an inactive field would otherwise open nothing: show inactive
  // ones, once. Adjusted while rendering, as React advises for state that
  // follows from data that has just arrived.
  const [linkHandled, setLinkHandled] = useState(false)
  if (linkedField && !linkHandled && defs.length > 0) {
    setLinkHandled(true)
    if (defs.find((d) => d.id === linkedField)?.active === false) setShowInactive(true)
  }

  const scrolled = useRef(false)
  useEffect(() => {
    if (!linkedField || scrolled.current || defs.length === 0) return
    scrolled.current = true
    document.getElementById(`cf-${linkedField}`)?.scrollIntoView?.({ block: 'start' })
  }, [linkedField, defs])

  const isVisible = (d: FieldDef) => d.active || showInactive
  const visible = defs.filter(isVisible)

  const reorderMutation = useMutation({
    mutationFn: (ids: string[]) => reorderFieldDefs(ids),
    onMutate: async (ids) => {
      await qc.cancelQueries({ queryKey: FIELDS_KEY, exact: true })
      const previous = qc.getQueryData<FieldDef[]>(FIELDS_KEY)
      if (previous) {
        qc.setQueryData<FieldDef[]>(FIELDS_KEY, ids.flatMap((id, n) => {
          const d = previous.find((x) => x.id === id)
          return d ? [{ ...d, sort_order: n + 1 }] : []
        }))
      }
      setReorderError('')
      return { previous }
    },
    onError: (err, _ids, ctx) => {
      if (ctx?.previous) qc.setQueryData(FIELDS_KEY, ctx.previous)
      setReorderError(extractError(err))
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: FIELDS_KEY, exact: true })
      // Field numbers order the fields on both forms.
      invalidateForms(qc)
    },
  })

  function toggle(id: string) {
    setFocusPlaceFor(null)
    setExpanded((s) => {
      const next = new Set(s)
      if (!next.delete(id)) next.add(id)
      return next
    })
  }

  // The row is opened once the refetched list has it: a POST response has no
  // place_count and is never put in the list.
  async function handleCreated(def: FieldDef) {
    await qc.invalidateQueries({ queryKey: FIELDS_KEY, exact: true })
    setAdding(false)
    setCreated({ id: def.id, name: def.name })
    setExpanded((s) => new Set(s).add(def.id))
    setFocusPlaceFor(def.id)
  }

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex flex-col items-start gap-3 sm:flex-row sm:justify-between sm:gap-0">
          <div className="min-w-0">
            <h1 className="text-2xl font-bold text-gray-900">Custom Fields</h1>
            <p className="mt-1 text-sm text-gray-500">
              Extra questions on a ticket. A field appears only where you attach it: to a category, a type or an item.
            </p>
          </div>
          <div className="flex shrink-0 flex-col items-start gap-2 sm:ml-6 sm:items-end">
            <Button onClick={() => setAdding(true)} className={BTN_TOUCH}>
              <PlusIcon aria-hidden="true" className="mr-2 h-4 w-4" />
              New field
            </Button>
            <label className={CHECK_LABEL}>
              <input type="checkbox" checked={showInactive} onChange={(e) => setShowInactive(e.target.checked)} />
              Show inactive
            </label>
          </div>
        </div>

        {created && (
          <CreatedBanner
            name={created.name}
            placeCount={defs.find((d) => d.id === created.id)?.place_count}
            onDismiss={() => setCreated(null)}
          />
        )}

        {adding && <CreateForm defs={defs} onCreated={handleCreated} onCancel={() => setAdding(false)} />}

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : isError ? (
          <p role="alert" className="text-sm text-red-600">Could not load custom fields.</p>
        ) : (
          <div className="space-y-2">
            <p className="text-sm text-gray-500">
              Drag to set the order fields appear in. On a ticket, fields still appear by level: the category's first, then the type's, then the item's.
            </p>
            {reorderError && <p role="alert" className="text-sm text-red-600">{reorderError}</p>}
            {visible.length === 0 ? (
              <p className="rounded-lg border bg-white px-4 py-10 text-center text-sm text-gray-400">
                No custom fields defined yet.
              </p>
            ) : (
              <SortableList
                className="rounded-lg border bg-white"
                items={visible.map((d) => ({ id: d.id, label: d.name, def: d }))}
                onReorder={(ids) => reorderMutation.mutate(mergeOrder(defs, isVisible, ids))}
                renderItem={({ def }, handle) => (
                  <FieldRow
                    def={def}
                    handle={handle}
                    expanded={expanded.has(def.id)}
                    onToggle={() => toggle(def.id)}
                    autoFocusAdd={focusPlaceFor === def.id}
                    alignWithInactive={showInactive}
                  />
                )}
              />
            )}
          </div>
        )}
      </div>
    </Layout>
  )
}
