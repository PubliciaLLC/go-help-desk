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
} from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { ChevronRightIcon, ChevronDownIcon, PlusIcon, TrashIcon, GripVerticalIcon, PencilIcon, CheckIcon, XIcon } from 'lucide-react'
import type { Category, TicketType, TicketItem } from '@/api/types'

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
    <div
      ref={setNodeRef}
      style={style}
      className={`group flex items-center gap-2 py-2 pl-3 pr-3 hover:bg-gray-50 ${isDragging ? 'opacity-50' : ''}`}
    >
      <button
        {...attributes}
        {...listeners}
        className="cursor-grab text-gray-200 hover:text-gray-400 active:cursor-grabbing"
        title="Drag to reorder"
      >
        <GripVerticalIcon className="h-3.5 w-3.5" />
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
    // Optimistic update
    qc.setQueryData(['admin', 'items', categoryId, type.id], reordered)
    // Persist new sort orders
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
              Define the three-level classification hierarchy used to categorize tickets: Category → Type → Item.
              All three levels are optional — a ticket may use only a Category, or a Category + Type, or all three.
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
