import { useEffect, useState } from 'react'
import { useParams } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { addGuestReply, getGuestTicket, GuestLinkInvalid } from '@/api/guest'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

// The ticket a guest reached by link.
//
// The token arrives in the path because a link has to be clickable, and is
// taken out of the address bar immediately: a token left in the URL leaks
// through Referer on every outbound link and through any access log in front
// of the app. It lives in component state from then on and goes back to the
// server in a header.
export function GuestTicketViewPage() {
  const { token } = useParams({ from: '/g/$token' })
  const qc = useQueryClient()
  const [reply, setReply] = useState('')

  useEffect(() => {
    // replaceState, not pushState: the token must not survive in history
    // either, and the back button should leave the page rather than return to
    // a URL that still carries it.
    window.history.replaceState(null, '', '/g')
  }, [])

  const { data: ticket, isLoading, error } = useQuery({
    queryKey: ['guest-ticket', token],
    queryFn: () => getGuestTicket(token),
    retry: false,
  })

  const send = useMutation({
    mutationFn: () => addGuestReply(token, reply.trim()),
    onSuccess: () => {
      setReply('')
      qc.invalidateQueries({ queryKey: ['guest-ticket', token] })
    },
  })

  if (isLoading) {
    return <Shell><p className="text-sm text-gray-500">Loading…</p></Shell>
  }

  // One message for every reason. The server does not say which, deliberately,
  // and repeating a guess back to the visitor would undo that.
  if (error || !ticket) {
    const expired = error instanceof GuestLinkInvalid
    return (
      <Shell>
        <Card>
          <CardHeader><CardTitle className="text-lg">This link no longer works</CardTitle></CardHeader>
          <CardContent className="space-y-3 text-sm text-gray-700">
            <p>
              {expired
                ? 'Links are replaced each time your ticket is updated, and stop working when a ticket is closed.'
                : 'We could not open that ticket.'}
            </p>
            <p>
              If your ticket is still open, you can{' '}
              <a href="/track" className="text-blue-600 underline">request a new link</a>{' '}
              with your tracking number and email address.
            </p>
          </CardContent>
        </Card>
      </Shell>
    )
  }

  return (
    <Shell>
      <Card>
        <CardHeader>
          <div className="flex items-baseline justify-between gap-3 flex-wrap">
            <CardTitle className="text-lg">{ticket.subject}</CardTitle>
            <span className="text-xs font-mono text-gray-500">{ticket.tracking_number}</span>
          </div>
          <p className="text-sm text-gray-500 mt-1">Status: {ticket.status}</p>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm whitespace-pre-wrap text-gray-800">{ticket.description}</p>

          <div className="space-y-3 border-t pt-4">
            {ticket.replies.length === 0 && (
              <p className="text-sm text-gray-500">No replies yet.</p>
            )}
            {ticket.replies.map((r) => (
              <div
                key={r.id}
                className={
                  r.from_you
                    ? 'rounded-md bg-blue-50 p-3 text-sm'
                    : 'rounded-md bg-gray-100 p-3 text-sm'
                }
              >
                <p className="text-xs text-gray-500 mb-1">
                  {/* Staff are described, never named: the server sends no
                      identity for a reply that is not the guest's own. */}
                  {r.from_you ? 'You' : 'Support'} ·{' '}
                  {new Date(r.created_at).toLocaleString()}
                </p>
                <p className="whitespace-pre-wrap text-gray-800">{r.body}</p>
              </div>
            ))}
          </div>

          <form
            className="space-y-2 border-t pt-4"
            onSubmit={(e) => {
              e.preventDefault()
              if (reply.trim()) send.mutate()
            }}
          >
            <Textarea
              value={reply}
              onChange={(e) => setReply(e.target.value)}
              rows={4}
              placeholder="Add a reply…"
              aria-label="Add a reply"
            />
            {send.isError && (
              <p className="text-sm text-red-600">
                We could not add your reply. The ticket may have been closed.
              </p>
            )}
            <Button type="submit" disabled={!reply.trim() || send.isPending}>
              {send.isPending ? 'Sending…' : 'Send reply'}
            </Button>
          </form>
        </CardContent>
      </Card>
    </Shell>
  )
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-screen bg-gray-50 flex items-start justify-center p-4">
      <div className="max-w-2xl w-full mt-8">{children}</div>
    </div>
  )
}
