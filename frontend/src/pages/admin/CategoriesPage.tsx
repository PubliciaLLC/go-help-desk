import { useState, useRef, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  DndContext, closestCenter, PointerSensor, useSensor, useSensors,
  type DragEndEvent,
} from '@dnd-kit/core'
import {
  SortableContext, useSortable, verticalListSortingStrategy, arrayMove,
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import {
  listCategories, createCategory, updateCategory, deleteCategory,
  listTypes, createType, updateType, deleteType,
  listItems, createItem, updateItem, deleteItem,
  listGroups, addGroupScope, removeGroupScope,
  listGroupsForCategory, listGroupsForType,
  listFieldDefs,
  listCategoryAssignments, listTypeAssignments, listItemAssignments,
  createCategoryAssignment, createTypeAssignment, createItemAssignment,
  updateCategoryAssignment, updateTypeAssignment, updateItemAssignment,
  deleteCategoryAssignment, deleteTypeAssignment, deleteItemAssignment,
} from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import {
  ChevronRightIcon, ChevronDownIcon, PlusIcon, TrashIcon,
  GripVerticalIcon, PencilIcon, CheckIcon, XIcon, UsersRoundIcon, SlidersIcon,
} from 'lucide-react'
import type { Category, TicketType, TicketItem, Group, Assignment } from '@/api/types'

// ── Inline name editor ────────────────────────────────────────────────────────

function InlineName({
  value,
  onSave,
  className,
}: {
  value: string
  onSave: (name: string) => Promise<unknown>
  className?: string
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)
  const [saving, setSaving] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (editing) inputRef.current?.select()
  }, [editing])

  async function commit() {
    const trimmed = draft.trim()
    if (!trimmed || trimmed === value) { cancel(); return }
    setSaving(true)
    try {
      await onSave(trimmed)
      setEditing(false)
    } finally {
      setSaving(false)
    }
  }

  function cancel() {
    setDraft(value)
    setEditing(false)
  }

  if (editing) {
    return (
      <div className="flex flex-1 items-center gap-1" onClick={(e) => e.stopPropagation()}>
        <Input
          ref={inputRef}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') commit()
            if (e.key === 'Escape') cancel()
          }}
          className="h-6 py-0 text-sm"
          disabled={saving}
        />
        <button
          onClick={commit}
          disabled={saving || !draft.trim()}
          className="text-green-600 hover:text-green-700 disabled:opacity-40"
          title="Save"
        >
          <CheckIcon className="h-3.5 w-3.5" />
        </button>
        <button onClick={cancel} className="text-gray-400 hover:text-gray-600" title="Cancel">
          <XIcon className="h-3.5 w-3.5" />
        </button>
      </div>
    )
  }

  return (
    <span className={`group/name flex flex-1 items-center gap-1 ${className ?? ''}`}>
      <span>{value}</span>
      <button
        onClick={(e) => { e.stopPropagation(); setDraft(value); setEditing(true) }}
        className="invisible text-gray-300 hover:text-gray-600 group-hover/name:visible"
        title="Rename"
      >
        <PencilIcon className="h-3 w-3" />
      </button>
    </span>
  )
}

// ── Groups subsection ─────────────────────────────────────────────────────────

function GroupsSubsection({
  categoryId,
  typeId,
}: {
  categoryId: string
  typeId?: string
}) {
  const qc = useQueryClient()
  const [addingGroup, setAddingGroup] = useState(false)
  const [selectedGroupId, setSelectedGroupId] = useState('')

  const queryKey = typeId
    ? ['ctiGroups', categoryId, typeId]
    : ['ctiGroups', categoryId]

  const { data: assignedGroups = [] } = useQuery<Group[]>({
    queryKey,
    queryFn: () => typeId
      ? listGroupsForType(categoryId, typeId)
      : listGroupsForCategory(categoryId),
  })

  const { data: allGroups = [] } = useQuery<Group[]>({
    queryKey: ['admin', 'groups'],
    queryFn: listGroups,
    enabled: addingGroup,
  })

  const addMutation = useMutation({
    mutationFn: () => addGroupScope(selectedGroupId, { category_id: categoryId, type_id: typeId }),
    onSuccess: () => {
      setAddingGroup(false)
      setSelectedGroupId('')
      qc.invalidateQueries({ queryKey })
    },
  })

  const removeMutation = useMutation({
    mutationFn: (groupId: string) => removeGroupScope(groupId, { category_id: categoryId, type_id: typeId }),
    onSuccess: () => qc.invalidateQueries({ queryKey }),
  })

  const assignedIds = new Set(assignedGroups.map((g) => g.id))
  const available = allGroups.filter((g) => !assignedIds.has(g.id))

  return (
    <div className="px-3 py-2 border-t border-gray-100">
      <div className="flex items-center gap-1.5 mb-2">
        <UsersRoundIcon className="h-3 w-3 text-gray-400" />
        <span className="text-xs font-semibold uppercase tracking-wide text-gray-400">Groups</span>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {assignedGroups.map((g) => (
          <span
            key={g.id}
            className="inline-flex items-center gap-1 rounded-full bg-blue-50 px-2.5 py-0.5 text-xs font-medium text-blue-700"
          >
            {g.name}
            <button
              onClick={() => removeMutation.mutate(g.id)}
              className="text-blue-400 hover:text-blue-700"
              title="Remove group"
            >
              <XIcon className="h-3 w-3" />
            </button>
          </span>
        ))}
        {addingGroup ? (
          <div className="flex items-center gap-1.5">
            <select
              autoFocus
              className="rounded border border-gray-300 bg-white px-2 py-0.5 text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
              value={selectedGroupId}
              onChange={(e) => setSelectedGroupId(e.target.value)}
            >
              <option value="">Select group…</option>
              {available.map((g) => (
                <option key={g.id} value={g.id}>{g.name}</option>
              ))}
            </select>
            <Button
              size="sm"
              className="h-6 px-2 text-xs"
              onClick={() => addMutation.mutate()}
              disabled={!selectedGroupId || addMutation.isPending}
            >
              Add
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="h-6 px-2 text-xs"
              onClick={() => { setAddingGroup(false); setSelectedGroupId('') }}
            >
              Cancel
            </Button>
          </div>
        ) : (
          <button
            onClick={() => setAddingGroup(true)}
            className="inline-flex items-center gap-1 rounded-full border border-dashed border-gray-300 px-2.5 py-0.5 text-xs text-gray-400 hover:border-blue-300 hover:text-blue-600"
          >
            <PlusIcon className="h-3 w-3" /> Add group
          </button>
        )}
      </div>
    </div>
  )
}

// ── Fields subsection ─────────────────────────────────────────────────────────

type FieldsScope =
  | { kind: 'category'; categoryId: string }
  | { kind: 'type'; categoryId: string; typeId: string }
  | { kind: 'item'; categoryId: string; typeId: string; itemId: string }

function FieldsSubsection({ scope }: { scope: FieldsScope }) {
  const qc = useQueryClient()
  const [addingField, setAddingField] = useState(false)
  const [selectedDefId, setSelectedDefId] = useState('')

  const queryKey =
    scope.kind === 'category'
      ? ['ctiFields', scope.categoryId]
      : scope.kind === 'type'
      ? ['ctiFields', scope.categoryId, scope.typeId]
      : ['ctiFields', scope.categoryId, scope.typeId, scope.itemId]

  const { data: assignments = [] } = useQuery<Assignment[]>({
    queryKey,
    queryFn: () => {
      if (scope.kind === 'category') return listCategoryAssignments(scope.categoryId)
      if (scope.kind === 'type') return listTypeAssignments(scope.categoryId, scope.typeId)
      return listItemAssignments(scope.categoryId, scope.typeId, scope.itemId)
    },
  })

  const { data: allDefs = [] } = useQuery({
    queryKey: ['admin', 'custom-fields'],
    queryFn: listFieldDefs,
    enabled: addingField,
  })

  const assignedDefIds = new Set(assignments.map((a) => a.field_def_id))
  const available = allDefs.filter((d) => d.active && !assignedDefIds.has(d.id))

  const addMutation = useMutation({
    mutationFn: () => {
      const input = { field_def_id: selectedDefId, sort_order: assignments.length + 1, visible_on_new: true, required_on_new: false }
      if (scope.kind === 'category') return createCategoryAssignment(scope.categoryId, input)
      if (scope.kind === 'type') return createTypeAssignment(scope.categoryId, scope.typeId, input)
      return createItemAssignment(scope.categoryId, scope.typeId, scope.itemId, input)
    },
    onSuccess: () => {
      setAddingField(false)
      setSelectedDefId('')
      qc.invalidateQueries({ queryKey })
    },
  })

  function removeAssignment(assignmentId: string) {
    if (scope.kind === 'category') return deleteCategoryAssignment(scope.categoryId, assignmentId)
    if (scope.kind === 'type') return deleteTypeAssignment(scope.categoryId, scope.typeId, assignmentId)
    return deleteItemAssignment(scope.categoryId, scope.typeId, scope.itemId, assignmentId)
  }

  function updateAssignment(assignmentId: string, patch: { visible_on_new?: boolean; required_on_new?: boolean }) {
    if (scope.kind === 'category') return updateCategoryAssignment(scope.categoryId, assignmentId, patch)
    if (scope.kind === 'type') return updateTypeAssignment(scope.categoryId, scope.typeId, assignmentId, patch)
    return updateItemAssignment(scope.categoryId, scope.typeId, scope.itemId, assignmentId, patch)
  }

  const removeMutation = useMutation({
    mutationFn: (assignmentId: string) => removeAssignment(assignmentId),
    onSuccess: () => qc.invalidateQueries({ queryKey }),
  })

  const toggleMutation = useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: { visible_on_new?: boolean; required_on_new?: boolean } }) =>
      updateAssignment(id, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey }),
  })

  if (assignments.length === 0 && !addingField) {
    return (
      <div className="px-3 py-2 border-t border-gray-100">
        <div className="flex items-center gap-1.5 mb-1">
          <SlidersIcon className="h-3 w-3 text-gray-400" />
          <span className="text-xs font-semibold uppercase tracking-wide text-gray-400">Fields</span>
        </div>
        <button
          onClick={() => setAddingField(true)}
          className="inline-flex items-center gap-1 rounded-full border border-dashed border-gray-300 px-2.5 py-0.5 text-xs text-gray-400 hover:border-blue-300 hover:text-blue-600"
        >
          <PlusIcon className="h-3 w-3" /> Assign field
        </button>
      </div>
    )
  }

  return (
    <div className="px-3 py-2 border-t border-gray-100">
      <div className="flex items-center gap-1.5 mb-2">
        <SlidersIcon className="h-3 w-3 text-gray-400" />
        <span className="text-xs font-semibold uppercase tracking-wide text-gray-400">Fields</span>
      </div>
      <div className="space-y-1">
        {(assignments as Assignment[]).map((a) => (
          <div key={a.id} className="group flex items-center gap-2 rounded px-2 py-1 hover:bg-gray-50 text-xs">
            <span className="flex-1 font-medium text-gray-700">
              {a.field_def?.name ?? a.field_def_id}
              <span className="ml-1.5 text-gray-400 font-normal">{a.field_def?.field_type}</span>
            </span>
            <label className="flex items-center gap-1 text-gray-500 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={a.visible_on_new}
                onChange={(e) => toggleMutation.mutate({ id: a.id, patch: { visible_on_new: e.target.checked } })}
                className="h-3 w-3"
              />
              visible
            </label>
            <label className="flex items-center gap-1 text-gray-500 cursor-pointer select-none">
              <input
                type="checkbox"
                checked={a.required_on_new}
                onChange={(e) => toggleMutation.mutate({ id: a.id, patch: { required_on_new: e.target.checked } })}
                className="h-3 w-3"
              />
              required
            </label>
            <button
              onClick={() => removeMutation.mutate(a.id)}
              className="invisible text-gray-300 hover:text-red-500 group-hover:visible"
              title="Remove assignment"
            >
              <XIcon className="h-3.5 w-3.5" />
            </button>
          </div>
        ))}
        {addingField ? (
          <div className="flex items-center gap-1.5 pt-1">
            <select
              autoFocus
              className="rounded border border-gray-300 bg-white px-2 py-0.5 text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
              value={selectedDefId}
              onChange={(e) => setSelectedDefId(e.target.value)}
            >
              <option value="">Select field…</option>
              {available.map((d) => (
                <option key={d.id} value={d.id}>{d.name} ({d.field_type})</option>
              ))}
            </select>
            <Button
              size="sm"
              className="h-6 px-2 text-xs"
              onClick={() => addMutation.mutate()}
              disabled={!selectedDefId || addMutation.isPending}
            >
              Assign
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="h-6 px-2 text-xs"
              onClick={() => { setAddingField(false); setSelectedDefId('') }}
            >
              Cancel
            </Button>
          </div>
        ) : (
          <button
            onClick={() => setAddingField(true)}
            className="inline-flex items-center gap-1 rounded-full border border-dashed border-gray-300 px-2.5 py-0.5 text-xs text-gray-400 hover:border-blue-300 hover:text-blue-600"
          >
            <PlusIcon className="h-3 w-3" /> Assign field
          </button>
        )}
      </div>
    </div>
  )
}

// ── Sortable item row ─────────────────────────────────────────────────────────

function SortableItemRow({
  item,
  categoryId,
  typeId,
}: {
  item: TicketItem
  categoryId: string
  typeId: string
}) {
  const qc = useQueryClient()
  const [expanded, setExpanded] = useState(false)
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: item.id })

  const style = { transform: CSS.Transform.toString(transform), transition }

  const renameMutation = useMutation({
    mutationFn: (name: string) => updateItem(categoryId, typeId, item.id, { name }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'items', categoryId, typeId] }),
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteItem(categoryId, typeId, item.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'items', categoryId, typeId] }),
  })

  return (
    <div ref={setNodeRef} style={style} className={isDragging ? 'opacity-50' : ''}>
      <div className="group flex items-center gap-2 py-2 pl-3 pr-3 hover:bg-gray-50">
        <button
          {...attributes}
          {...listeners}
          className="cursor-grab text-gray-200 hover:text-gray-400 active:cursor-grabbing"
          title="Drag to reorder"
        >
          <GripVerticalIcon className="h-3.5 w-3.5" />
        </button>
        <button
          onClick={() => setExpanded((v) => !v)}
          className="flex h-4 w-4 shrink-0 items-center justify-center text-gray-400 hover:text-gray-600"
        >
          {expanded ? <ChevronDownIcon className="h-3.5 w-3.5" /> : <ChevronRightIcon className="h-3.5 w-3.5" />}
        </button>
        <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-gray-300" />
        <InlineName
          value={item.name}
          onSave={(name) => renameMutation.mutateAsync(name)}
          className="text-sm text-gray-700"
        />
        <button
          onClick={() => deleteMutation.mutate()}
          disabled={deleteMutation.isPending}
          className="invisible text-gray-300 hover:text-red-500 group-hover:visible"
          title="Delete item"
        >
          <TrashIcon className="h-3.5 w-3.5" />
        </button>
      </div>
      {expanded && (
        <div className="ml-8 border-l border-gray-100 pl-2">
          <FieldsSubsection scope={{ kind: 'item', categoryId, typeId, itemId: item.id }} />
        </div>
      )}
    </div>
  )
}

// ── Sortable type row ─────────────────────────────────────────────────────────

function SortableTypeRow({
  type,
  categoryId,
}: {
  type: TicketType
  categoryId: string
}) {
  const qc = useQueryClient()
  const [expanded, setExpanded] = useState(false)
  const [addingItem, setAddingItem] = useState(false)
  const [itemName, setItemName] = useState('')

  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: type.id })
  const style = { transform: CSS.Transform.toString(transform), transition }

  const { data: items = [], isLoading } = useQuery({
    queryKey: ['admin', 'items', categoryId, type.id],
    queryFn: () => listItems(categoryId, type.id),
    enabled: expanded,
  })

  const renameMutation = useMutation({
    mutationFn: (name: string) => updateType(categoryId, type.id, { name }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'types', categoryId] }),
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteType(categoryId, type.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'types', categoryId] }),
  })

  const addItemMutation = useMutation({
    mutationFn: () => createItem(categoryId, type.id, { name: itemName.trim(), sort_order: items.length + 1 }),
    onSuccess: () => {
      setItemName('')
      setAddingItem(false)
      qc.invalidateQueries({ queryKey: ['admin', 'items', categoryId, type.id] })
    },
  })

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  async function handleItemDragEnd(event: DragEndEvent) {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIndex = items.findIndex((i) => i.id === active.id)
    const newIndex = items.findIndex((i) => i.id === over.id)
    const reordered = arrayMove(items, oldIndex, newIndex)
    qc.setQueryData(['admin', 'items', categoryId, type.id], reordered)
    await Promise.all(
      reordered.map((item, idx) =>
        updateItem(categoryId, type.id, item.id, { sort_order: idx + 1 })
      )
    )
    qc.invalidateQueries({ queryKey: ['admin', 'items', categoryId, type.id] })
  }

  return (
    <div ref={setNodeRef} style={style} className={isDragging ? 'opacity-50' : ''}>
      <div className="group flex items-center gap-2 py-2.5 pl-2 pr-3 hover:bg-gray-50">
        <button
          {...attributes}
          {...listeners}
          className="cursor-grab text-gray-200 hover:text-gray-400 active:cursor-grabbing"
          title="Drag to reorder"
        >
          <GripVerticalIcon className="h-3.5 w-3.5" />
        </button>
        <button
          onClick={() => setExpanded((v) => !v)}
          className="flex h-5 w-5 shrink-0 items-center justify-center text-gray-400 hover:text-gray-600"
        >
          {expanded ? <ChevronDownIcon className="h-4 w-4" /> : <ChevronRightIcon className="h-4 w-4" />}
        </button>
        <InlineName
          value={type.name}
          onSave={(name) => renameMutation.mutateAsync(name)}
          className="text-sm font-medium text-gray-800"
        />
        <button
          onClick={() => deleteMutation.mutate()}
          disabled={deleteMutation.isPending}
          className="invisible text-gray-300 hover:text-red-500 group-hover:visible"
          title="Delete type"
        >
          <TrashIcon className="h-3.5 w-3.5" />
        </button>
      </div>

      {expanded && (
        <div className="ml-8 border-l border-gray-100 pl-2 pb-1">
          <GroupsSubsection categoryId={categoryId} typeId={type.id} />
          <FieldsSubsection scope={{ kind: 'type', categoryId, typeId: type.id }} />
          <div className="pt-1">
            {isLoading ? (
              <div className="py-2 pl-4"><Spinner /></div>
            ) : (
              <>
                <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleItemDragEnd}>
                  <SortableContext items={items.map((i) => i.id)} strategy={verticalListSortingStrategy}>
                    {items.map((item) => (
                      <SortableItemRow key={item.id} item={item} categoryId={categoryId} typeId={type.id} />
                    ))}
                  </SortableContext>
                </DndContext>
                {addingItem ? (
                  <div className="flex items-center gap-2 py-2 pl-4 pr-3">
                    <Input
                      autoFocus
                      placeholder="Item name"
                      value={itemName}
                      onChange={(e) => setItemName(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' && itemName.trim()) addItemMutation.mutate()
                        if (e.key === 'Escape') { setAddingItem(false); setItemName('') }
                      }}
                      className="h-7 text-sm"
                    />
                    <Button size="sm" className="h-7 text-xs" onClick={() => addItemMutation.mutate()} disabled={!itemName.trim() || addItemMutation.isPending}>Add</Button>
                    <Button size="sm" variant="ghost" className="h-7 text-xs" onClick={() => { setAddingItem(false); setItemName('') }}>Cancel</Button>
                  </div>
                ) : (
                  <button
                    onClick={() => setAddingItem(true)}
                    className="flex items-center gap-1.5 py-2 pl-5 text-xs text-gray-400 hover:text-gray-600"
                  >
                    <PlusIcon className="h-3 w-3" /> Add item
                  </button>
                )}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// ── Sortable category row ─────────────────────────────────────────────────────

function SortableCategoryRow({ cat }: { cat: Category }) {
  const qc = useQueryClient()
  const [expanded, setExpanded] = useState(false)
  const [addingType, setAddingType] = useState(false)
  const [typeName, setTypeName] = useState('')

  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: cat.id })
  const style = { transform: CSS.Transform.toString(transform), transition }

  const { data: types = [], isLoading } = useQuery({
    queryKey: ['admin', 'types', cat.id],
    queryFn: () => listTypes(cat.id),
    enabled: expanded,
  })

  const renameMutation = useMutation({
    mutationFn: (name: string) => updateCategory(cat.id, { name }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'categories'] }),
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteCategory(cat.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'categories'] }),
  })

  const addTypeMutation = useMutation({
    mutationFn: () => createType(cat.id, { name: typeName.trim(), sort_order: types.length + 1 }),
    onSuccess: () => {
      setTypeName('')
      setAddingType(false)
      qc.invalidateQueries({ queryKey: ['admin', 'types', cat.id] })
    },
  })

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  async function handleTypeDragEnd(event: DragEndEvent) {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIndex = types.findIndex((t) => t.id === active.id)
    const newIndex = types.findIndex((t) => t.id === over.id)
    const reordered = arrayMove(types, oldIndex, newIndex)
    qc.setQueryData(['admin', 'types', cat.id], reordered)
    await Promise.all(
      reordered.map((tp, idx) => updateType(cat.id, tp.id, { sort_order: idx + 1 }))
    )
    qc.invalidateQueries({ queryKey: ['admin', 'types', cat.id] })
  }

  return (
    <div ref={setNodeRef} style={style} className={`border-b last:border-b-0 ${isDragging ? 'opacity-50' : ''}`}>
      <div className="group flex items-center gap-2 px-4 py-3 hover:bg-gray-50">
        <button
          {...attributes}
          {...listeners}
          className="cursor-grab text-gray-200 hover:text-gray-400 active:cursor-grabbing"
          title="Drag to reorder"
        >
          <GripVerticalIcon className="h-4 w-4" />
        </button>
        <button
          onClick={() => setExpanded((v) => !v)}
          className="flex h-5 w-5 shrink-0 items-center justify-center text-gray-500 hover:text-gray-700"
        >
          {expanded ? <ChevronDownIcon className="h-4 w-4" /> : <ChevronRightIcon className="h-4 w-4" />}
        </button>
        <InlineName
          value={cat.name}
          onSave={(name) => renameMutation.mutateAsync(name)}
          className="text-sm font-semibold text-gray-900"
        />
        {!cat.active && (
          <span className="rounded bg-yellow-100 px-1.5 py-0.5 text-xs text-yellow-700">inactive</span>
        )}
        <button
          onClick={() => deleteMutation.mutate()}
          disabled={deleteMutation.isPending}
          className="invisible text-gray-300 hover:text-red-500 group-hover:visible"
          title="Delete category"
        >
          <TrashIcon className="h-4 w-4" />
        </button>
      </div>

      {expanded && (
        <div className="ml-11 border-l border-gray-100 pl-2 pb-2">
          <GroupsSubsection categoryId={cat.id} />
          <FieldsSubsection scope={{ kind: 'category', categoryId: cat.id }} />
          <div className="pt-1">
            {isLoading ? (
              <div className="py-3"><Spinner /></div>
            ) : (
              <>
                <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleTypeDragEnd}>
                  <SortableContext items={types.map((t) => t.id)} strategy={verticalListSortingStrategy}>
                    {types.map((type) => (
                      <SortableTypeRow key={type.id} type={type} categoryId={cat.id} />
                    ))}
                  </SortableContext>
                </DndContext>
                {addingType ? (
                  <div className="flex items-center gap-2 py-2 pl-3 pr-3">
                    <Input
                      autoFocus
                      placeholder="Type name"
                      value={typeName}
                      onChange={(e) => setTypeName(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' && typeName.trim()) addTypeMutation.mutate()
                        if (e.key === 'Escape') { setAddingType(false); setTypeName('') }
                      }}
                      className="h-8 text-sm"
                    />
                    <Button size="sm" className="h-8 text-xs" onClick={() => addTypeMutation.mutate()} disabled={!typeName.trim() || addTypeMutation.isPending}>Add</Button>
                    <Button size="sm" variant="ghost" className="h-8 text-xs" onClick={() => { setAddingType(false); setTypeName('') }}>Cancel</Button>
                  </div>
                ) : (
                  <button
                    onClick={() => setAddingType(true)}
                    className="flex items-center gap-1.5 py-2 pl-3 text-xs text-gray-400 hover:text-gray-600"
                  >
                    <PlusIcon className="h-3.5 w-3.5" /> Add type
                  </button>
                )}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export function CategoriesPage() {
  const qc = useQueryClient()
  const [addingCategory, setAddingCategory] = useState(false)
  const [catName, setCatName] = useState('')
  const [formError, setFormError] = useState('')

  const { data: categories = [], isLoading } = useQuery({
    queryKey: ['admin', 'categories'],
    queryFn: listCategories,
  })

  const addCategoryMutation = useMutation({
    mutationFn: () => createCategory({ name: catName.trim(), sort_order: categories.length + 1 }),
    onSuccess: () => {
      setCatName('')
      setAddingCategory(false)
      setFormError('')
      qc.invalidateQueries({ queryKey: ['admin', 'categories'] })
    },
    onError: (err) => setFormError(extractError(err)),
  })

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  const sorted = [...categories].sort((a, b) => a.sort_order - b.sort_order)

  async function handleCategoryDragEnd(event: DragEndEvent) {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIndex = sorted.findIndex((c) => c.id === active.id)
    const newIndex = sorted.findIndex((c) => c.id === over.id)
    const reordered = arrayMove(sorted, oldIndex, newIndex)
    qc.setQueryData(['admin', 'categories'], reordered)
    await Promise.all(
      reordered.map((cat, idx) => updateCategory(cat.id, { sort_order: idx + 1 }))
    )
    qc.invalidateQueries({ queryKey: ['admin', 'categories'] })
  }

  return (
    <Layout>
      <div className="space-y-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900">Categories</h1>
            <p className="mt-1 text-sm text-gray-500">
              Define the three-level classification hierarchy: Category → Type → Item.
              Expand any node to manage its linked groups and custom field assignments.
            </p>
          </div>
          <Button onClick={() => setAddingCategory(true)} className="ml-6 shrink-0">
            <PlusIcon className="mr-2 h-4 w-4" />
            New Category
          </Button>
        </div>

        {addingCategory && (
          <div className="rounded-lg border bg-white p-4">
            <p className="mb-3 text-sm font-medium text-gray-700">New category</p>
            <div className="flex items-center gap-3">
              <Input
                autoFocus
                placeholder="Category name"
                value={catName}
                onChange={(e) => setCatName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && catName.trim()) addCategoryMutation.mutate()
                  if (e.key === 'Escape') { setAddingCategory(false); setCatName('') }
                }}
                className="max-w-xs"
              />
              <Button onClick={() => addCategoryMutation.mutate()} disabled={!catName.trim() || addCategoryMutation.isPending}>
                {addCategoryMutation.isPending ? 'Adding…' : 'Add'}
              </Button>
              <Button variant="outline" onClick={() => { setAddingCategory(false); setCatName('') }}>Cancel</Button>
            </div>
            {formError && <p className="mt-2 text-sm text-red-600">{formError}</p>}
          </div>
        )}

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : categories.length === 0 ? (
          <div className="rounded-lg border border-dashed bg-white px-6 py-12 text-center">
            <p className="text-sm text-gray-500">No categories yet. Add one to get started.</p>
          </div>
        ) : (
          <div className="rounded-lg border bg-white overflow-hidden">
            <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleCategoryDragEnd}>
              <SortableContext items={sorted.map((c) => c.id)} strategy={verticalListSortingStrategy}>
                {sorted.map((cat) => (
                  <SortableCategoryRow key={cat.id} cat={cat} />
                ))}
              </SortableContext>
            </DndContext>
          </div>
        )}
      </div>
    </Layout>
  )
}
