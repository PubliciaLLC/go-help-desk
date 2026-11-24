import { useId, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { XIcon } from 'lucide-react'
import {
  addTicketFollower,
  followTicket,
  listTicketFollowers,
  removeTicketFollower,
  unfollowTicket,
} from '@/api/tickets'
import { listAssignableStaff } from '@/api/admin'
import { extractError, extractErrorCode } from '@/api/client'
import type { TicketFollower } from '@/api/types'
import { useAuthStore } from '@/store/auth'
import { Button } from '@/components/ui/button'
import { Select } from '@/components/ui/select'
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
//
// #695: staff and administrators can also add another staff member or
// administrator and remove any follower. That does NOT answer at once: the
// server may refuse on scope (the person cannot see the ticket), so the list
// changes only after the server agrees, and the refusal is shown here. The
// picker is the staff list the assignee picker uses, minus people already
// following and the viewer (the Follow button is for me); the server decides
// who may really be added.

export function FollowersPanel({ ticketId }: { ticketId: string }) {
  const me = useAuthStore((s) => s.user)
  const qc = useQueryClient()
  const key = ['followers', ticketId]
  const [error, setError] = useState('')
  const [selected, setSelected] = useState('')
  const pickerId = useId()

  const { data: staff = [] } = useQuery({
    queryKey: ['assignable-staff'],
    queryFn: listAssignableStaff,
  })

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

  const refreshOthers = () => {
    qc.invalidateQueries({ queryKey: key })
    qc.invalidateQueries({ queryKey: ['ticketAudit', ticketId] })
  }

  const add = useMutation({
    mutationFn: (userId: string) => addTicketFollower(ticketId, userId),
    onMutate: () => setError(''),
    onSuccess: () => setSelected(''),
    onError: (err, userId) => {
      const name = staff.find((p) => p.id === userId)?.display_name ?? 'That person'
      const code = extractErrorCode(err)
      setError(
        code === 'follower_cannot_view_ticket'
          ? `${name} cannot see this ticket, so they cannot follow it.`
          : code === 'follower_not_eligible'
            ? `${name} cannot follow tickets: they must be an active staff member or administrator.`
            : extractError(err),
      )
    },
    onSettled: refreshOthers,
  })

  const remove = useMutation({
    mutationFn: (userId: string) => removeTicketFollower(ticketId, userId),
    onMutate: () => setError(''),
    onError: (err) => setError(extractError(err)),
    onSettled: refreshOthers,
  })

  // Who can be picked: assignable staff who are not already following, and not me.
  const followingIds = new Set(followers.map((f) => f.user_id))
  const candidates = staff.filter((p) => p.assignable && p.id !== me?.id && !followingIds.has(p.id))
  const busy = add.isPending || remove.isPending

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
            <li key={f.user_id} className="flex items-start justify-between gap-2 text-xs text-gray-700">
              <span className="min-w-0 wrap-anywhere">
                <span className="font-medium">{f.display_name}</span>
                {me?.id === f.user_id && <span className="text-gray-400"> (you)</span>}
              </span>
              {/* My own row has the Unfollow button below. */}
              {me?.id !== f.user_id && (
                <button
                  type="button"
                  aria-label={`Remove ${f.display_name} as a follower`}
                  className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-gray-500 hover:bg-gray-100 hover:text-gray-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-50"
                  onClick={() => remove.mutate(f.user_id)}
                  disabled={busy}
                >
                  <XIcon aria-hidden="true" className="h-3.5 w-3.5" />
                </button>
              )}
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
        <div className="space-y-1">
          <label htmlFor={pickerId} className="text-xs text-gray-500">Add follower</label>
          <div className="flex gap-2">
            <Select
              id={pickerId}
              className="h-8 min-w-0 flex-1 text-xs"
              value={selected}
              onChange={(e) => setSelected(e.target.value)}
              disabled={busy}
            >
              <option value="">Select a person…</option>
              {candidates.map((p) => (
                <option key={p.id} value={p.id}>{p.display_name}</option>
              ))}
            </Select>
            <Button
              size="sm"
              className="h-8 text-xs"
              onClick={() => add.mutate(selected)}
              disabled={!selected || busy}
            >
              Add
            </Button>
          </div>
        </div>
        {error && <p role="alert" className="text-xs text-red-600">{error}</p>}
      </CardContent>
    </Card>
  )
}
