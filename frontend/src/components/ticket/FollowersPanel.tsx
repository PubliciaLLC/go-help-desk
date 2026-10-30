import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { followTicket, listTicketFollowers, unfollowTicket } from '@/api/tickets'
import { extractError } from '@/api/client'
import type { TicketFollower } from '@/api/types'
import { useAuthStore } from '@/store/auth'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

// #441: who follows this ticket, and a Follow / Unfollow button for the
// viewer. Staff and admin only (the page does not render it for anyone else;
// the API refuses a requester too). A follower is emailed about the ticket's
// activity under their "activity" switch, so the button is a personal choice
// and only ever changes the viewer's own row.
//
// The button answers at once and puts the list back if the server refuses:
// following is cheap to undo, so waiting on the network before the button
// moves would feel slower than the thing deserves.

export function FollowersPanel({ ticketId }: { ticketId: string }) {
  const me = useAuthStore((s) => s.user)
  const qc = useQueryClient()
  const key = ['followers', ticketId]
  const [error, setError] = useState('')

  const { data: followers = [], isLoading, isError } = useQuery({
    queryKey: key,
    queryFn: () => listTicketFollowers(ticketId),
  })

  const following = !!me && followers.some((f) => f.user_id === me.id)

  const toggle = useMutation({
    mutationFn: (follow: boolean) => (follow ? followTicket(ticketId) : unfollowTicket(ticketId)),
    onMutate: async (follow: boolean) => {
      setError('')
      // A refetch landing now would overwrite the guess below with the old
      // answer for a moment.
      await qc.cancelQueries({ queryKey: key })
      const previous = qc.getQueryData<TicketFollower[]>(key) ?? []
      if (me) {
        qc.setQueryData<TicketFollower[]>(
          key,
          follow
            ? [...previous.filter((f) => f.user_id !== me.id), { user_id: me.id, display_name: me.display_name, created_at: new Date().toISOString() }]
            : previous.filter((f) => f.user_id !== me.id),
        )
      }
      return { previous }
    },
    onError: (err, _follow, ctx) => {
      if (ctx) qc.setQueryData(key, ctx.previous)
      setError(extractError(err))
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: key })
      // Following is audited; the activity feed beside this has a new entry.
      qc.invalidateQueries({ queryKey: ['ticketAudit', ticketId] })
    },
  })

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
          Followers
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {isLoading && <p className="text-xs text-gray-400">Loading…</p>}
        {isError && <p className="text-xs text-red-600">Could not load followers</p>}
        {!isLoading && !isError && followers.length === 0 && (
          <p className="text-xs text-gray-400">Nobody is following this ticket.</p>
        )}
        <ul className="space-y-1">
          {followers.map((f) => (
            <li key={f.user_id} className="text-xs text-gray-700 wrap-anywhere">
              <span className="font-medium">{f.display_name}</span>
              {me?.id === f.user_id && <span className="text-gray-400"> (you)</span>}
            </li>
          ))}
        </ul>
        <Button
          variant="outline"
          size="sm"
          className="h-7 text-xs"
          onClick={() => toggle.mutate(!following)}
          disabled={toggle.isPending || isLoading || isError}
        >
          {following ? 'Unfollow' : 'Follow'}
        </Button>
        {error && <p role="alert" className="text-xs text-red-600">{error}</p>}
      </CardContent>
    </Card>
  )
}
