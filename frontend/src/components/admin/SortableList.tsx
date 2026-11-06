import { useRef, type ReactNode } from 'react'
import {
  DndContext, closestCenter, KeyboardSensor, PointerSensor, useSensor, useSensors,
  type Announcements, type DragEndEvent,
} from '@dnd-kit/core'
import {
  SortableContext, useSortable, verticalListSortingStrategy, arrayMove, sortableKeyboardCoordinates,
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { GripVerticalIcon } from 'lucide-react'
import { TOUCH_SIZE } from './touchTargets'

export interface SortableItem {
  id: string
  // Names the item in the handle's label and in what a screen reader is told.
  label: string
}

interface SortableListProps<T extends SortableItem> {
  items: T[]
  // The ids in their new order. The list holds no order of its own: the caller
  // stores it and passes the items back in that order.
  onReorder: (ids: string[]) => void
  // `handle` is the reorder button; the caller puts it where it belongs in the row.
  renderItem: (item: T, handle: ReactNode) => ReactNode
  className?: string
}

const INSTRUCTIONS =
  'To reorder, press Space to pick up, the arrow keys to move, Space to drop, or Escape to cancel.'

// dnd-kit's own announcements say "draggable item 1"; these name the item.
function describe(items: SortableItem[]) {
  return {
    labelOf: (id: string | number) => items.find((i) => i.id === id)?.label ?? 'item',
    position: (id: string | number) => `position ${items.findIndex((i) => i.id === id) + 1} of ${items.length}`,
  }
}

function Row<T extends SortableItem>({
  item, renderItem,
}: {
  item: T
  renderItem: SortableListProps<T>['renderItem']
}) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } =
    useSortable({ id: item.id })
  const handle = (
    <button
      type="button"
      ref={setActivatorNodeRef}
      {...attributes}
      {...listeners}
      aria-label={`Reorder ${item.label}`}
      // touch-none: without it the browser takes the gesture to scroll the page
      // and the drag never starts on a phone.
      className={`touch-none cursor-grab rounded text-gray-400 hover:text-gray-600 active:cursor-grabbing ${TOUCH_SIZE}`}
    >
      <GripVerticalIcon aria-hidden="true" className="h-4 w-4" />
    </button>
  )
  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={isDragging ? 'relative z-10 opacity-60' : undefined}
    >
      {renderItem(item, handle)}
    </li>
  )
}

export function SortableList<T extends SortableItem>({ items, onReorder, renderItem, className }: SortableListProps<T>) {
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  // Where the item was last announced as being over. Measured in Chromium
  // (e2e/custom-fields.spec.ts), a keyboard drop's onDragEnd carries an `over`
  // one move behind what was announced, which would drop the item back where it
  // started. onDragOver is the event that matches the announcement, so the drop
  // follows it. Cleared on the next pick-up, not at the drop, so the "Dropped"
  // announcement can read it too.
  const lastOver = useRef<string | null>(null)

  const { labelOf, position } = describe(items)
  const announcements: Announcements = {
    onDragStart: ({ active }) => `Picked up ${labelOf(active.id)}, ${position(active.id)}.`,
    // Starting a drag reports "over" the item itself; that would replace "Picked up".
    onDragOver: ({ active, over }) =>
      over && over.id !== active.id ? `${labelOf(active.id)} is now over ${labelOf(over.id)}, ${position(over.id)}.` : undefined,
    onDragEnd: ({ active, over }) => {
      const target = lastOver.current ?? over?.id
      return target ? `Dropped ${labelOf(active.id)}, ${position(target)}.` : `Dropped ${labelOf(active.id)}.`
    },
    onDragCancel: ({ active }) => `Reorder cancelled. ${labelOf(active.id)} is back where it was.`,
  }

  function handleDragEnd({ active, over }: DragEndEvent) {
    const target = lastOver.current ?? (over ? String(over.id) : null)
    if (!target || active.id === target) return
    const from = items.findIndex((i) => i.id === active.id)
    const to = items.findIndex((i) => i.id === target)
    if (from < 0 || to < 0) return
    onReorder(arrayMove(items, from, to).map((i) => i.id))
  }

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      onDragStart={() => { lastOver.current = null }}
      onDragOver={({ over }) => { lastOver.current = over ? String(over.id) : null }}
      onDragEnd={handleDragEnd}
      accessibility={{ announcements, screenReaderInstructions: { draggable: INSTRUCTIONS } }}
    >
      <SortableContext items={items.map((i) => i.id)} strategy={verticalListSortingStrategy}>
        <ul className={className}>
          {items.map((item) => (
            <Row key={item.id} item={item} renderItem={renderItem} />
          ))}
        </ul>
      </SortableContext>
    </DndContext>
  )
}
