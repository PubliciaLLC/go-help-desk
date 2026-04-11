import { useState } from 'react'
import { useParams } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getTicket, listReplies, addReply, resolveTicket, reopenTicket } from '@/api/tickets'
import { listStatuses } from '@/api/admin'
import { extractError } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

function priorityVariant(p: string) {
  if (p === 'critical') return 'destructive'
  if (p === 'high') return 'warning'
  if (p === 'medium') return 'default'
  return 'secondary'
}

function formatDate(iso: string) {
  return new Date(iso).toLocaleString()
}

export function TicketDetailPage() {
  const { id } = useParams({ from: '/tickets/$id' })
  const { user } = useAuthStore()
  const qc = useQueryClient()

  const [replyBody, setReplyBody] = useState('')
  const [replyInternal, setReplyInternal] = useState(false)
  const [replyError, setReplyError] = useState('')

  const { data: ticket, isLoading, error } = useQuery({
    queryKey: ['ticket', id],
    queryFn: () => getTicket(id),
  })

  const { data: replies = [] } = useQuery({
    queryKey: ['replies', id],
    queryFn: () => listReplies(id),
    enabled: !!ticket,
  })

  const { data: statuses = [] } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

  const statusName = statuses.find((s) => s.id === ticket?.status_id)?.name ?? '…'
  const statusColor = statuses.find((s) => s.id === ticket?.status_id)?.color

  const replyMutation = useMutation({
    mutationFn: () => addReply(id, replyBody, replyInternal),
    onSuccess: () => {
      setReplyBody('')
      setReplyError('')
      qc.invalidateQueries({ queryKey: ['replies', id] })
      qc.invalidateQueries({ queryKey: ['ticket', id] })
    },
    onError: (err) => setReplyError(extractError(err)),
  })

  const resolveMutation = useMutation({
    mutationFn: () => resolveTicket(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['ticket', id] }),
  })

  const reopenMutation = useMutation({
    mutationFn: () => reopenTicket(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['ticket', id] }),
  })

  if (isLoading) return <Layout><div className="flex justify-center py-12"><Spinner size="lg" /></div></Layout>
  if (error || !ticket) return <Layout><p className="text-red-600">Ticket not found.</p></Layout>

  const canResolve = (user?.role === 'staff' || user?.role === 'admin') && statusName !== 'Resolved' && statusName !== 'Closed'
  const canReopen = (user?.role === 'staff' || user?.role === 'admin') && (statusName === 'Resolved' || statusName === 'Closed')

  return (
    <Layout>
      <div className="space-y-6">
        {/* Header */}
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm text-gray-500">
              <span>{ticket.tracking_number}</span>
              <span>·</span>
              <span>Opened {formatDate(ticket.created_at)}</span>
            </div>
            <h1 className="text-2xl font-bold text-gray-900">{ticket.subject}</h1>
            <div className="flex items-center gap-2 flex-wrap">
              <span
                className="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-medium"
                style={{ borderColor: statusColor, color: statusColor }}
              >
                <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: statusColor }} />
                {statusName}
              </span>
              <Badge variant={priorityVariant(ticket.priority) as never}>
                {ticket.priority}
              </Badge>
            </div>
          </div>

          <div className="flex gap-2 shrink-0">
            {canResolve && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => resolveMutation.mutate()}
                disabled={resolveMutation.isPending}
              >
                Mark Resolved
              </Button>
            )}
            {canReopen && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => reopenMutation.mutate()}
                disabled={reopenMutation.isPending}
              >
                Reopen
              </Button>
            )}
          </div>
        </div>

        {/* Description */}
        <Card>
          <CardHeader>
            <CardTitle className="text-sm text-gray-500">Description</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="whitespace-pre-wrap text-sm">{ticket.description || <span className="text-gray-400">No description provided.</span>}</p>
          </CardContent>
        </Card>

        {/* Thread */}
        <div className="space-y-3">
          <h2 className="text-base font-semibold text-gray-900">Replies ({replies.length})</h2>
          {replies.map((r) => (
            <div
              key={r.id}
              className={`rounded-lg border p-4 text-sm ${r.internal ? 'border-yellow-200 bg-yellow-50' : 'bg-white'}`}
            >
              <div className="mb-1 flex items-center justify-between text-xs text-gray-500">
                <span>{r.author_id ?? 'System'}</span>
                <span className="flex items-center gap-2">
                  {r.internal && <span className="text-yellow-600 font-medium">Internal note</span>}
                  {formatDate(r.created_at)}
                </span>
              </div>
              <p className="whitespace-pre-wrap">{r.body}</p>
            </div>
          ))}
        </div>

        {/* Reply form */}
        {statusName !== 'Closed' && (
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">Add reply</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <Textarea
                placeholder="Type your reply…"
                rows={4}
                value={replyBody}
                onChange={(e) => setReplyBody(e.target.value)}
              />
              {(user?.role === 'staff' || user?.role === 'admin') && (
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={replyInternal}
                    onChange={(e) => setReplyInternal(e.target.checked)}
                    className="h-4 w-4 rounded border-gray-300"
                  />
                  Internal note (not visible to user)
                </label>
              )}
              {replyError && <p className="text-sm text-red-600">{replyError}</p>}
              <Button
                onClick={() => replyMutation.mutate()}
                disabled={replyMutation.isPending || !replyBody.trim()}
              >
                {replyMutation.isPending ? 'Sending…' : 'Send reply'}
              </Button>
            </CardContent>
          </Card>
        )}
      </div>
    </Layout>
  )
}
