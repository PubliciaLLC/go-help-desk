import { useEffect, useId, useRef, useState } from 'react'
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
  // What a screen reader is told when an add or a remove went through. The
  // region is always in the page: one that appears with its text is often not
  // announced.
  const [announcement, setAnnouncement] = useState('')
  const pickerId = useId()
  const pickerRef = useRef<HTMLSelectElement>(null)
  const listRef = useRef<HTMLUListElement>(null)
  // Where keyboard focus goes once the list has been redrawn: the picker, or
  // the remove button now at this position (the row that followed the one
  // removed). Disabling or removing the focused button drops focus on the page,
  // so it is put back by hand, as ServerNoticeBar does after a Hide.
  const focusNext = useRef<{ index: number } | 'picker' | null>(null)
  const [focusTick, setFocusTick] = useState(0)
  const moveFocus = (to: { index: number } | 'picker') => {
    focusNext.current = to
    setFocusTick((t) => t + 1)
  }

  useEffect(() => {
    const to = focusNext.current
    if (to === null) return
    focusNext.current = null
    const rows = listRef.current?.querySelectorAll<HTMLButtonElement>('[data-remove]')
    const target = to !== 'picker' ? rows?.[to.index] : undefined
    ;(target ?? pickerRef.current)?.focus()
  }, [focusTick])

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

  // Awaited, so focus moves after the list is the server's answer.
  const refreshOthers = () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: key }),
      qc.invalidateQueries({ queryKey: ['ticketAudit', ticketId] }),
    ])
  const nameOf = (userId: string) => staff.find((p) => p.id === userId)?.display_name ?? 'That person'

  const add = useMutation({
    mutationFn: (userId: string) => addTicketFollower(ticketId, userId),
    onMutate: () => {
      setError('')
      setAnnouncement('')
    },
    onSuccess: (_data, userId) => {
      setSelected('')
      setAnnouncement(`Added ${nameOf(userId)} as a follower`)
    },
    onError: (err, userId) => {
      const name = nameOf(userId)
      const code = extractErrorCode(err)
      // Somebody who cannot follow should not stay on offer.
      if (code === 'follower_not_eligible') qc.invalidateQueries({ queryKey: ['assignable-staff'] })
      setError(
        code === 'follower_cannot_view_ticket'
          ? `${name} cannot see this ticket, so they cannot follow it.`
          : code === 'follower_not_eligible'
            ? `${name} cannot follow tickets: they must be an active staff member or administrator.`
            : extractError(err),
      )
    },
    onSettled: async () => {
      await refreshOthers()
      // The Add button was disabled while the request ran, which dropped focus.
      moveFocus('picker')
    },
  })

  const remove = useMutation({
    mutationFn: (v: { userId: string; index: number }) => removeTicketFollower(ticketId, v.userId),
    onMutate: () => {
      setError('')
      setAnnouncement('')
    },
    onSuccess: async (_data, v) => {
      setAnnouncement(`Removed ${nameOf(v.userId)} as a follower`)
      await refreshOthers()
      moveFocus({ index: v.index })
    },
    onError: (err) => {
      setError(extractError(err))
      void refreshOthers()
    },
  })

  // Who can be picked: assignable staff who are not already following, and not me.
  const followingIds = new Set(followers.map((f) => f.user_id))
  const candidates = staff.filter((p) => p.assignable && p.id !== me?.id && !followingIds.has(p.id))
  const busy = add.isPending || remove.isPending
  // Until the list is known the picker would offer people who already follow.
  const listKnown = !isLoading && !isError

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-xs font-semibold uppercase tracking-wider text-fg-faint">
          Followers
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {isLoading && <p className="text-xs text-fg-faint">Loading…</p>}
        {isError && <p className="text-xs text-danger-fg">Could not load followers</p>}
        {!isLoading && !isError && followers.length === 0 && (
          <p className="text-xs text-fg-faint">Nobody is following this ticket.</p>
        )}
        <ul ref={listRef} className="space-y-1">
          {followers.map((f) => (
            <li key={f.user_id} className="flex items-start justify-between gap-2 text-xs text-fg-soft">
              <span className="min-w-0 wrap-anywhere">
                <span className="font-medium">{f.display_name}</span>
                {me?.id === f.user_id && <span className="text-fg-faint"> (you)</span>}
              </span>
              {/* My own row has the Unfollow button below. */}
              {me?.id !== f.user_id && (
                <button
                  type="button"
                  data-remove
                  aria-label={`Remove ${f.display_name} as a follower`}
                  aria-disabled={busy || undefined}
                  className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-fg-faint hover:bg-muted hover:text-fg-soft focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 aria-disabled:opacity-50 pointer-coarse:h-11 pointer-coarse:w-11"
                  // Not `disabled`: that would take focus off the button.
                  onClick={(e) => {
                    if (busy) return
                    const index = Array.from(listRef.current?.querySelectorAll('[data-remove]') ?? []).indexOf(e.currentTarget)
                    remove.mutate({ userId: f.user_id, index })
                  }}
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
          <label htmlFor={pickerId} className="text-xs text-fg-faint">Person to add as follower</label>
          <div className="flex gap-2">
            <Select
              id={pickerId}
              ref={pickerRef}
              className="h-8 min-w-0 flex-1 text-xs pointer-coarse:h-11"
              value={selected}
              onChange={(e) => setSelected(e.target.value)}
              disabled={!listKnown || add.isPending}
            >
              <option value="">Select a person…</option>
              {candidates.map((p) => (
                <option key={p.id} value={p.id}>{p.display_name}</option>
              ))}
            </Select>
            <Button
              size="sm"
              className="h-8 text-xs pointer-coarse:h-11"
              aria-label="Add follower"
              onClick={() => add.mutate(selected)}
              disabled={!selected || !listKnown || busy}
            >
              Add
            </Button>
          </div>
        </div>
        <p role="status" aria-live="polite" className="sr-only">{announcement}</p>
        {error && <p role="alert" className="text-xs text-danger-fg">{error}</p>}
      </CardContent>
    </Card>
  )
}
