import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { updateTicket } from '@/api/tickets'
import { extractError } from '@/api/client'
import { Select } from '@/components/ui/select'
import type { Priority, Ticket } from '@/api/types'

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
// reporting user). The select shows the ticket's own priority, so a refused
// change leaves the old value showing. After a successful change it shows the
// chosen value until the ticket has reloaded: without that it fell back to the
// old value for the length of the refetch, which read as the change failing.
export function PriorityControl({ ticketId, priority, locked = false }: PriorityControlProps) {
  const qc = useQueryClient()
  const [chosen, setChosen] = useState<Priority | null>(null)

  // The reload caught up: from here the ticket's own value is the one to show.
  // Adjusted during render rather than in an effect, so there is no frame that
  // shows the stale choice.
  if (chosen !== null && priority === chosen) setChosen(null)

  const mutation = useMutation({
    mutationFn: (next: Priority) => updateTicket(ticketId, { priority: next }),
    onMutate: (next) => setChosen(next),
    onSuccess: async (_, next) => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ['ticket', ticketId] }),
        qc.invalidateQueries({ queryKey: ['ticketAudit', ticketId] }),
        qc.invalidateQueries({ queryKey: ['tickets'] }),
      ])
      // The reload finished. When it brought the chosen value the line above
      // lets go of it; when it did not (a concurrent change, or no ticket query
      // to reload), show what is true instead of holding on to the choice.
      if (qc.getQueryData<Ticket>(['ticket', ticketId])?.priority !== next) setChosen(null)
    },
    onError: () => setChosen(null),
  })

  return (
    <div className="space-y-1">
      <Select
        aria-label="Ticket priority"
        className="h-8 w-full text-xs"
        value={chosen ?? priority}
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
