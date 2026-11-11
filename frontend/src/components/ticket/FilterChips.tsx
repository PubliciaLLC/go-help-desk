import type { ReactNode } from 'react'
import { XIcon } from 'lucide-react'
import { useRequesterById } from '@/lib/useRequesterById'
import { isFilterActive, type FilterKey, type TicketSearch } from '@/lib/ticketSearch'

// How the chips name an id. Each returns undefined while its list is loading
// or when the id is not in it. The page owns the lists; the chips only read.
export interface ChipNames {
  status: (id: string) => string | undefined
  category: (id: string) => string | undefined
  type: (id: string) => string | undefined
  item: (id: string) => string | undefined
  user: (id: string) => string | undefined
  group: (id: string) => string | undefined
}

const LABEL: Record<FilterKey, string> = {
  status: 'Status',
  priority: 'Priority',
  requester: 'Requester',
  category: 'Category',
  type: 'Category',
  item: 'Category',
  assignee: 'Assignee',
  group: 'Group',
  created: 'Created',
  updated: 'Updated',
  channel: 'Came in by',
  sla: 'SLA',
}

// In the order the panel lists them. type and item are part of the category
// chip: a type means nothing without the category above it.
const ORDER: FilterKey[] = ['status', 'priority', 'requester', 'category', 'assignee', 'group', 'created', 'updated', 'channel', 'sla']

// The filters a requester is sent. The page drops the rest from the request, so
// a chip for one would claim a filter that is not applied.
const STAFF_ONLY: FilterKey[] = ['requester', 'assignee', 'group', 'updated', 'channel', 'sla']

const CHANNEL_LABEL: Record<string, string> = { web: 'Web', guest: 'Email / guest', phone: 'Phone', api: 'API', mcp: 'MCP' }
const SLA_LABEL = { breached: 'Breached', at_risk: 'At risk' }

const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

function day(ymd: string, withYear: boolean) {
  const [y, m, d] = ymd.split('-').map(Number)
  return new Date(y, m - 1, d).toLocaleDateString(undefined, { day: 'numeric', month: 'short', ...(withYear ? { year: 'numeric' } : {}) })
}

function range(from?: string, to?: string): string {
  if (from && to) {
    const sameYear = from.slice(0, 4) === to.slice(0, 4)
    return `${day(from, !sameYear)} – ${day(to, true)}`
  }
  if (from) return `from ${day(from, true)}`
  return `to ${day(to!, true)}`
}

function RequesterName({ id }: { id: string }) {
  const { data, isError, isPending } = useRequesterById(id)
  if (isPending) return <>…</>
  return <>{isError || data === null ? 'Unknown account' : data.display_name}</>
}

export function FilterChips({
  search,
  names,
  staff,
  onClear,
  onClearAll,
}: {
  search: TicketSearch
  names: ChipNames
  staff: boolean
  onClear: (key: FilterKey) => void
  onClearAll: () => void
}) {
  const shown = ORDER.filter(k => isFilterActive(search, k) && (staff || !STAFF_ONLY.includes(k)))
  if (shown.length === 0) return null

  const value = (k: FilterKey): ReactNode => {
    switch (k) {
      case 'status':
        return search.status!.map(id => names.status(id) ?? '…').join(', ')
      case 'priority':
        return search.priority!.map(cap).join(', ')
      case 'requester':
        // An account wins: it is what the page sends when an address has both.
        return search.reporter ? <RequesterName id={search.reporter} /> : `“${search.requester}”`
      case 'category':
        return [
          search.category && (names.category(search.category) ?? '…'),
          search.type && (names.type(search.type) ?? '…'),
          search.item && (names.item(search.item) ?? '…'),
        ]
          .filter(Boolean)
          .join(' › ')
      case 'assignee':
        return names.user(search.assignee!) ?? '…'
      case 'group':
        return names.group(search.group!) ?? '…'
      case 'created':
        return range(search.created_from, search.created_to)
      case 'updated':
        return range(search.updated_from, search.updated_to)
      case 'channel':
        return search.channel!.map(c => CHANNEL_LABEL[c] ?? c).join(', ')
      case 'sla':
        return SLA_LABEL[search.sla!]
      default:
        return null
    }
  }

  return (
    <div role="group" aria-label="Active filters" className="flex flex-wrap items-center gap-x-2 gap-y-2 text-sm">
      <ul role="list" className="contents">
        {shown.map(k => (
          <li
            key={k}
            className="inline-flex max-w-full items-center gap-1.5 rounded-full border border-gray-300 bg-gray-100 py-0.5 pl-2.5 pr-1.5 text-xs font-medium text-gray-700 pointer-coarse:min-h-11"
          >
            <span className="min-w-0 wrap-anywhere">
              <span className="font-normal text-gray-500">
                {k === 'requester' && !search.reporter ? 'Requester contains:' : `${LABEL[k]}:`}
              </span>{' '}
              {value(k)}
            </span>
            <button
              type="button"
              aria-label={`Remove ${LABEL[k]} filter`}
              onClick={() => onClear(k)}
              className="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-gray-500 hover:bg-gray-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 pointer-coarse:h-9 pointer-coarse:w-9"
            >
              <XIcon aria-hidden="true" className="h-3 w-3" />
            </button>
          </li>
        ))}
      </ul>
      <button
        type="button"
        onClick={onClearAll}
        className="rounded-sm text-xs text-gray-500 underline underline-offset-2 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 pointer-coarse:min-h-11 pointer-coarse:px-2"
      >
        Clear all
      </button>
    </div>
  )
}
