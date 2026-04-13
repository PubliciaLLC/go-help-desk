import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  listCategories, createCategory, deleteCategory,
  listTypes, createType, deleteType,
  listItems, createItem, deleteItem,
} from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { ChevronRightIcon, ChevronDownIcon, PlusIcon, TrashIcon } from 'lucide-react'
import type { Category, TicketType, TicketItem } from '@/api/types'

// ── Item row ──────────────────────────────────────────────────────────────────

function ItemRow({
  item,
  categoryId,
  typeId,
}: {
  item: TicketItem
  categoryId: string
  typeId: string
}) {
  const qc = useQueryClient()

  const deleteMutation = useMutation({
    mutationFn: () => deleteItem(categoryId, typeId, item.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'items', categoryId, typeId] }),
  })

  return (
    <div className="group flex items-center gap-3 py-2 pl-4 pr-3 hover:bg-gray-50">
      <span className="h-1.5 w-1.5 rounded-full bg-gray-300" />
      <span className="flex-1 text-sm text-gray-700">{item.name}</span>
      <span className="text-xs text-gray-400">order {item.sort_order}</span>
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

// ── Type row ──────────────────────────────────────────────────────────────────

function TypeRow({
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

  const { data: items = [], isLoading } = useQuery({
    queryKey: ['admin', 'items', categoryId, type.id],
    queryFn: () => listItems(categoryId, type.id),
    enabled: expanded,
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

  return (
    <div>
      <div className="group flex items-center gap-2 py-2.5 pl-3 pr-3 hover:bg-gray-50">
        <button
          onClick={() => setExpanded((v) => !v)}
          className="flex h-5 w-5 shrink-0 items-center justify-center text-gray-400 hover:text-gray-600"
        >
          {expanded ? <ChevronDownIcon className="h-4 w-4" /> : <ChevronRightIcon className="h-4 w-4" />}
        </button>
        <span className="flex-1 text-sm font-medium text-gray-800">{type.name}</span>
        <span className="text-xs text-gray-400">order {type.sort_order}</span>
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
        <div className="ml-7 border-l border-gray-100 pl-2">
          {isLoading ? (
            <div className="py-2 pl-4"><Spinner /></div>
          ) : (
            <>
              {items.map((item) => (
                <ItemRow key={item.id} item={item} categoryId={categoryId} typeId={type.id} />
              ))}
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
                  <Button
                    size="sm"
                    className="h-7 text-xs"
                    onClick={() => addItemMutation.mutate()}
                    disabled={!itemName.trim() || addItemMutation.isPending}
                  >
                    Add
                  </Button>
                  <Button size="sm" variant="ghost" className="h-7 text-xs" onClick={() => { setAddingItem(false); setItemName('') }}>
                    Cancel
                  </Button>
                </div>
              ) : (
                <button
                  onClick={() => setAddingItem(true)}
                  className="flex items-center gap-1.5 py-2 pl-4 text-xs text-gray-400 hover:text-gray-600"
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

// ── Category row ──────────────────────────────────────────────────────────────

function CategoryRow({ cat }: { cat: Category }) {
  const qc = useQueryClient()
  const [expanded, setExpanded] = useState(false)
  const [addingType, setAddingType] = useState(false)
  const [typeName, setTypeName] = useState('')

  const { data: types = [], isLoading } = useQuery({
    queryKey: ['admin', 'types', cat.id],
    queryFn: () => listTypes(cat.id),
    enabled: expanded,
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

  return (
    <div className="border-b last:border-b-0">
      <div className="group flex items-center gap-2 px-4 py-3 hover:bg-gray-50">
        <button
          onClick={() => setExpanded((v) => !v)}
          className="flex h-5 w-5 shrink-0 items-center justify-center text-gray-500 hover:text-gray-700"
        >
          {expanded ? <ChevronDownIcon className="h-4 w-4" /> : <ChevronRightIcon className="h-4 w-4" />}
        </button>
        <span className="flex-1 text-sm font-semibold text-gray-900">{cat.name}</span>
        <span className="text-xs text-gray-400">order {cat.sort_order}</span>
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
        <div className="ml-9 border-l border-gray-100 pl-2 pb-2">
          {isLoading ? (
            <div className="py-3"><Spinner /></div>
          ) : (
            <>
              {types.map((type) => (
                <TypeRow key={type.id} type={type} categoryId={cat.id} />
              ))}
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
                  <Button
                    size="sm"
                    className="h-8 text-xs"
                    onClick={() => addTypeMutation.mutate()}
                    disabled={!typeName.trim() || addTypeMutation.isPending}
                  >
                    Add
                  </Button>
                  <Button size="sm" variant="ghost" className="h-8 text-xs" onClick={() => { setAddingType(false); setTypeName('') }}>
                    Cancel
                  </Button>
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
              <Button
                onClick={() => addCategoryMutation.mutate()}
                disabled={!catName.trim() || addCategoryMutation.isPending}
              >
                {addCategoryMutation.isPending ? 'Adding…' : 'Add'}
              </Button>
              <Button variant="outline" onClick={() => { setAddingCategory(false); setCatName('') }}>
                Cancel
              </Button>
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
            {categories
              .slice()
              .sort((a, b) => a.sort_order - b.sort_order)
              .map((cat) => (
                <CategoryRow key={cat.id} cat={cat} />
              ))}
          </div>
        )}
      </div>
    </Layout>
  )
}
