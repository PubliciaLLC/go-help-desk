import { useMutation, useQueryClient } from '@tanstack/react-query'
import { updateTicket } from '@/api/tickets'
import { extractError } from '@/api/client'
import { Select } from '@/components/ui/select'
import type { Priority } from '@/api/types'

const PRIORITIES: Priority[] = ['critical', 'high', 'medium', 'low']

interface PriorityControlProps {
  ticketId: string
  priority: Priority
  // locked: the ticket is Resolved or Closed. The server refuses a priority
  // change there (409 ticket_closed), so the select is shown disabled with the
  // reason rather than offering a choice that can only fail.
  locked?: boolean
}

// The caller decides who sees this (staff and admin; the API answers 403 to a
// reporting user). The select is controlled by the ticket's own priority, so a
// refused change leaves the old value showing.
export function PriorityControl({ ticketId, priority, locked = false }: PriorityControlProps) {
  const qc = useQueryClient()

  const mutation = useMutation({
    mutationFn: (next: Priority) => updateTicket(ticketId, { priority: next }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ticket', ticketId] })
      qc.invalidateQueries({ queryKey: ['ticketAudit', ticketId] })
      qc.invalidateQueries({ queryKey: ['tickets'] })
    },
  })

  return (
    <div className="space-y-1">
      <Select
        aria-label="Ticket priority"
        className="h-8 w-full text-xs"
        value={priority}
        onChange={(e) => mutation.mutate(e.target.value as Priority)}
        disabled={locked || mutation.isPending}
      >
        {PRIORITIES.map((p) => (
          <option key={p} value={p}>{p}</option>
        ))}
      </Select>
      {locked && (
        <p className="text-xs text-gray-500">
          Priority cannot be changed on a resolved or closed ticket.
        </p>
      )}
      {mutation.isError && (
        <p role="alert" className="text-xs text-red-600">{extractError(mutation.error)}</p>
      )}
    </div>
  )
}
