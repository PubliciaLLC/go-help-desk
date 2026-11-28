import { useState, type RefObject } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { XIcon } from 'lucide-react'
import type { Status } from '@/api/types'
import { Button } from '@/components/ui/button'
import { clearAll, type FilterKey, type TicketSearch } from '@/lib/ticketSearch'
import { TicketFilters } from './TicketFilters'

// The phone's version of the filter panel: a full-screen sheet whose changes
// are held as a draft and applied together with Show results. Closing it any
// other way throws the draft away, so a half-made choice never changes the list
// behind it.

function DialogBody({
  initial,
  onApply,
  returnFocusTo,
  ...rest
}: {
  initial: TicketSearch
  onApply: (draft: TicketSearch) => void
  returnFocusTo: RefObject<HTMLElement | null>
  staff: boolean
  slaOn: boolean
  statuses: Status[]
  offered?: readonly FilterKey[]
}) {
  const [draft, setDraft] = useState(initial)
  const patch = (p: Partial<TicketSearch>) => setDraft(d => ({ ...d, ...p }))

  return (
    <Dialog.Content
      aria-describedby={undefined}
      // Radix returns focus to its trigger, and this dialog has none (the page's
      // Filters button opens it by state), so without this focus falls to the
      // top of the document on Escape, Close and Show results alike.
      onCloseAutoFocus={e => {
        e.preventDefault()
        returnFocusTo.current?.focus()
      }}
      className="fixed inset-0 z-50 flex flex-col bg-card focus:outline-none"
    >
      <div className="flex items-center justify-between border-b border-border px-4 py-2">
        <Dialog.Title className="text-xl font-bold text-fg">Filters</Dialog.Title>
        <Dialog.Close
          aria-label="Close filters"
          className="inline-flex h-11 w-11 items-center justify-center rounded-md text-fg-muted hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <XIcon aria-hidden="true" className="h-5 w-5" />
        </Dialog.Close>
      </div>

      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-4 py-4">
        <TicketFilters value={draft} onChange={patch} layout="dialog" staff={rest.staff} slaOn={rest.slaOn} statuses={rest.statuses} offered={rest.offered} />
      </div>

      <div className="flex gap-3 border-t border-border px-4 py-3">
        <Button type="button" variant="outline" className="h-11 flex-1" onClick={() => setDraft(d => ({ ...clearAll(d) }))}>
          Clear all
        </Button>
        <Button type="button" className="h-11 flex-1" onClick={() => onApply(draft)}>
          Show results
        </Button>
      </div>
    </Dialog.Content>
  )
}

export function FilterDialog({
  open,
  onOpenChange,
  search,
  onApply,
  returnFocusTo,
  staff,
  slaOn,
  statuses,
  offered,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  search: TicketSearch
  onApply: (draft: TicketSearch) => void
  returnFocusTo: RefObject<HTMLElement | null>
  staff: boolean
  slaOn: boolean
  statuses: Status[]
  // The filters the view offers (VIEW_FILTERS); all of them when absent.
  offered?: readonly FilterKey[]
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        {/* Mounted only while open, so every opening starts from the address. */}
        <DialogBody
          initial={search}
          onApply={d => {
            onApply(d)
            onOpenChange(false)
          }}
          returnFocusTo={returnFocusTo}
          staff={staff}
          slaOn={slaOn}
          statuses={statuses}
          offered={offered}
        />
      </Dialog.Portal>
    </Dialog.Root>
  )
}
