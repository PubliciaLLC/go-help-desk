import { useState } from 'react'
import { useParams } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getTicket, listReplies, addReply, resolveTicket, reopenTicket, updateTicket } from '@/api/tickets'
import { TagInput } from '@/components/TagInput'
import { listStatuses, listUsers } from '@/api/admin'
import { extractError } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import { Spinner } from '@/components/ui/spinner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { api } from '@/api/client'
import type { Group, User } from '@/api/types'

function priorityVariant(p: string) {
  if (p === 'critical') return 'destructive'
  if (p === 'high') return 'warning'
  if (p === 'medium') return 'default'
  return 'secondary'
}

function formatDate(iso: string) {
  return new Date(iso).toLocaleString()
}

// Fetches the shared /api/v1/groups list (accessible to staff+admin).
async function listGroupsShared(): Promise<Group[]> {
  const res = await api.get<Group[]>('/groups')
  return res.data
}

// ── Assignee panel ────────────────────────────────────────────────────────────

interface AssigneePanelProps {
  ticketId: string
  assigneeUserId?: string
  assigneeGroupId?: string
  users: User[]
  groups: Group[]
  onUpdated: () => void
}

function AssigneePanel({ ticketId, assigneeUserId, assigneeGroupId, users, groups, onUpdated }: AssigneePanelProps) {
  const [mode, setMode] = useState<'user' | 'group'>('user')
  const [selectedId, setSelectedId] = useState('')
  const [error, setError] = useState('')

  const assignMutation = useMutation({
    mutationFn: () => {
      if (mode === 'user') {
        return updateTicket(ticketId, { assignee_user_id: selectedId || undefined, assignee_group_id: undefined })
      } else {
        return updateTicket(ticketId, { assignee_group_id: selectedId || undefined, assignee_user_id: undefined })
      }
    },
    onSuccess: () => {
      setSelectedId('')
      setError('')
      onUpdated()
    },
    onError: (err) => setError(extractError(err)),
  })

  const unassignMutation = useMutation({
    mutationFn: () => updateTicket(ticketId, { assignee_user_id: undefined, assignee_group_id: undefined }),
    onSuccess: () => { setError(''); onUpdated() },
    onError: (err) => setError(extractError(err)),
  })

  const currentUser = users.find((u) => u.id === assigneeUserId)
  const currentGroup = groups.find((g) => g.id === assigneeGroupId)

  const staffUsers = users.filter((u) => u.role === 'staff' || u.role === 'admin')

  return (
    <div className="space-y-2">
      {/* Current assignee */}
      <div className="text-sm">
        {currentUser ? (
          <span className="font-medium">{currentUser.display_name}</span>
        ) : currentGroup ? (
          <span className="inline-flex items-center gap-1 font-medium">
            <span className="h-2 w-2 rounded-full bg-blue-400" />
            {currentGroup.name}
          </span>
        ) : (
          <span className="text-gray-400">Unassigned</span>
        )}
      </div>

      {/* Assignment controls */}
      <div className="flex gap-1 text-xs">
        <button
          className={`px-2 py-0.5 rounded ${mode === 'user' ? 'bg-blue-100 text-blue-700 font-medium' : 'text-gray-500 hover:text-gray-700'}`}
          onClick={() => setMode('user')}
        >
          User
        </button>
        <button
          className={`px-2 py-0.5 rounded ${mode === 'group' ? 'bg-blue-100 text-blue-700 font-medium' : 'text-gray-500 hover:text-gray-700'}`}
          onClick={() => setMode('group')}
        >
          Group
        </button>
      </div>

      <div className="flex gap-2">
        <Select
          className="h-8 text-xs flex-1"
          value={selectedId}
          onChange={(e) => setSelectedId(e.target.value)}
        >
          <option value="">{mode === 'user' ? 'Select staff member…' : 'Select group…'}</option>
          {mode === 'user'
            ? staffUsers.map((u) => (
                <option key={u.id} value={u.id}>{u.display_name}</option>
              ))
            : groups.map((g) => (
                <option key={g.id} value={g.id}>{g.name}</option>
              ))}
        </Select>
        <Button
          size="sm"
          className="h-8 text-xs"
          onClick={() => assignMutation.mutate()}
          disabled={!selectedId || assignMutation.isPending}
        >
          Assign
        </Button>
      </div>

      {(assigneeUserId || assigneeGroupId) && (
        <button
          className="text-xs text-gray-400 hover:text-gray-600"
          onClick={() => unassignMutation.mutate()}
          disabled={unassignMutation.isPending}
        >
          Clear assignment
        </button>
      )}
      {error && <p className="text-xs text-red-600">{error}</p>}
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

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

  const isStaffOrAdmin = user?.role === 'staff' || user?.role === 'admin'

  const { data: allUsers = [] } = useQuery({
    queryKey: ['users'],
    queryFn: () => listUsers(),
    enabled: isStaffOrAdmin,
  })

  const { data: groups = [] } = useQuery({
    queryKey: ['groups-shared'],
    queryFn: listGroupsShared,
    enabled: isStaffOrAdmin,
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

  const canResolve = isStaffOrAdmin && statusName !== 'Resolved' && statusName !== 'Closed'
  const canReopen = isStaffOrAdmin && (statusName === 'Resolved' || statusName === 'Closed')

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

        <div className="grid grid-cols-3 gap-6">
          {/* Main column */}
          <div className="col-span-2 space-y-6">
            {/* Description */}
            <Card>
              <CardHeader>
                <CardTitle className="text-sm text-gray-500">Description</CardTitle>
              </CardHeader>
              <CardContent>
                <p className="whitespace-pre-wrap text-sm">
                  {ticket.description || <span className="text-gray-400">No description provided.</span>}
                </p>
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
                  {isStaffOrAdmin && (
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

          {/* Sidebar */}
          <div className="space-y-4">
            {isStaffOrAdmin && (
              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
                    Assignee
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <AssigneePanel
                    ticketId={id}
                    assigneeUserId={ticket.assignee_user_id}
                    assigneeGroupId={ticket.assignee_group_id}
                    users={allUsers}
                    groups={groups}
                    onUpdated={() => qc.invalidateQueries({ queryKey: ['ticket', id] })}
                  />
                </CardContent>
              </Card>
            )}

            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
                  Tags
                </CardTitle>
              </CardHeader>
              <CardContent>
                <TagInput ticketId={id} readonly={!isStaffOrAdmin} />
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
                  Details
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-2 text-sm">
                <div className="flex justify-between">
                  <span className="text-gray-500">Priority</span>
                  <Badge variant={priorityVariant(ticket.priority) as never}>{ticket.priority}</Badge>
                </div>
                <div className="flex justify-between">
                  <span className="text-gray-500">Created</span>
                  <span className="text-right text-xs">{formatDate(ticket.created_at)}</span>
                </div>
                {ticket.resolved_at && (
                  <div className="flex justify-between">
                    <span className="text-gray-500">Resolved</span>
                    <span className="text-right text-xs">{formatDate(ticket.resolved_at)}</span>
                  </div>
                )}
              </CardContent>
            </Card>
          </div>
        </div>
      </div>
    </Layout>
  )
}
