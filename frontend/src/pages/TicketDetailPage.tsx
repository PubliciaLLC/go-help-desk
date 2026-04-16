import { useState } from 'react'
import { useParams } from '@tanstack/react-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  getTicket,
  listReplies,
  listStatusHistory,
  addReply,
  resolveTicket,
  reopenTicket,
  updateTicket,
  listAttachments,
  uploadAttachment,
  attachmentDownloadUrl,
  listTicketCustomFields,
  putTicketCustomFields,
  listPublicCategories,
  listPublicTypes,
  listPublicItems,
} from '@/api/tickets'
import { TagInput } from '@/components/TagInput'
import { AttachmentUpload, type UploadState } from '@/components/AttachmentUpload'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { Group, User, StatusHistoryEntry, TicketFieldValue, Category, TicketType, TicketItem } from '@/api/types'

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

// ── Custom fields panel ───────────────────────────────────────────────────────

interface CustomFieldsPanelProps {
  ticketId: string
  isStaffOrAdmin: boolean
}

function CustomFieldsPanel({ ticketId, isStaffOrAdmin }: CustomFieldsPanelProps) {
  const qc = useQueryClient()
  const { data: values = [] } = useQuery<TicketFieldValue[]>({
    queryKey: ['customFields', ticketId],
    queryFn: () => listTicketCustomFields(ticketId),
  })

  const [editValues, setEditValues] = useState<Record<string, string> | null>(null)
  const [saveError, setSaveError] = useState('')

  const saveMutation = useMutation({
    mutationFn: () => putTicketCustomFields(ticketId, editValues!),
    onSuccess: () => {
      setEditValues(null)
      setSaveError('')
      qc.invalidateQueries({ queryKey: ['customFields', ticketId] })
    },
    onError: (err) => setSaveError(extractError(err)),
  })

  // Nothing to show if no values and not staff/admin (users only see populated values).
  if (values.length === 0 && !isStaffOrAdmin) return null
  if (values.length === 0) return null

  const displayValues = editValues
    ? values.map((v) => ({ ...v, value: editValues[v.field_def_id] ?? v.value }))
    : values

  function renderInput(v: TicketFieldValue) {
    const current = editValues?.[v.field_def_id] ?? v.value
    const onChange = (val: string) =>
      setEditValues((prev) => ({ ...(prev ?? {}), [v.field_def_id]: val }))

    switch (v.field_type) {
      case 'textarea':
        return (
          <Textarea
            value={current}
            onChange={(e) => onChange(e.target.value)}
            rows={2}
            className="text-xs"
          />
        )
      case 'number':
        return (
          <Input
            type="number"
            value={current}
            onChange={(e) => onChange(e.target.value)}
            className="h-7 text-xs"
          />
        )
      case 'select':
        return (
          <Select
            value={current}
            onChange={(e) => onChange(e.target.value)}
            className="h-7 text-xs"
          >
            <option value="">—</option>
            {(v.options ?? []).map((opt) => (
              <option key={opt} value={opt}>{opt}</option>
            ))}
          </Select>
        )
      default:
        return (
          <Input
            value={current}
            onChange={(e) => onChange(e.target.value)}
            className="h-7 text-xs"
          />
        )
    }
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
            Custom Fields
          </CardTitle>
          {isStaffOrAdmin && !editValues && (
            <button
              className="text-xs text-blue-600 hover:underline"
              onClick={() => {
                const init: Record<string, string> = {}
                for (const v of values) init[v.field_def_id] = v.value
                setEditValues(init)
              }}
            >
              Edit
            </button>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        {displayValues.map((v) => (
          <div key={v.field_def_id} className="space-y-0.5">
            <Label className="text-xs text-gray-500">{v.field_name}</Label>
            {isStaffOrAdmin && editValues ? (
              renderInput(v)
            ) : (
              <p className="text-sm">{v.value || <span className="text-gray-400">—</span>}</p>
            )}
          </div>
        ))}
        {isStaffOrAdmin && editValues && (
          <div className="flex gap-2 pt-1">
            <Button
              size="sm"
              className="h-7 text-xs"
              onClick={() => saveMutation.mutate()}
              disabled={saveMutation.isPending}
            >
              {saveMutation.isPending ? 'Saving…' : 'Save'}
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-xs"
              onClick={() => { setEditValues(null); setSaveError('') }}
            >
              Cancel
            </Button>
          </div>
        )}
        {saveError && <p className="text-xs text-red-600">{saveError}</p>}
      </CardContent>
    </Card>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export function TicketDetailPage() {
  const { id } = useParams({ from: '/tickets/$id' })
  const { user } = useAuthStore()
  const qc = useQueryClient()

  const [replyBody, setReplyBody] = useState('')
  const [replyInternal, setReplyInternal] = useState(false)
  const [replyNotify, setReplyNotify] = useState(true)
  const [replyFiles, setReplyFiles] = useState<File[]>([])
  const [replyUploadStates, setReplyUploadStates] = useState<Record<string, UploadState> | undefined>()
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

  const { data: statusHistory = [] } = useQuery({
    queryKey: ['statusHistory', id],
    queryFn: () => listStatusHistory(id),
    enabled: !!ticket,
  })

  type TimelineItem =
    | { kind: 'reply'; ts: string; data: typeof replies[0] }
    | { kind: 'status'; ts: string; data: StatusHistoryEntry }

  const timeline: TimelineItem[] = [
    ...replies.map((r) => ({ kind: 'reply' as const, ts: r.created_at, data: r })),
    ...statusHistory.map((h) => ({ kind: 'status' as const, ts: h.created_at, data: h })),
  ].sort((a, b) => new Date(a.ts).getTime() - new Date(b.ts).getTime())

  const { data: statuses = [] } = useQuery({
    queryKey: ['statuses'],
    queryFn: listStatuses,
  })

  const isStaffOrAdmin = user?.role === 'staff' || user?.role === 'admin'

  // ── CTI state ────────────────────────────────────────────────────────────────
  const [ctiEdit, setCtiEdit] = useState(false)
  const [ctiCategoryId, setCtiCategoryId] = useState('')
  const [ctiTypeId, setCtiTypeId] = useState('')
  const [ctiItemId, setCtiItemId] = useState('')
  const [ctiError, setCtiError] = useState('')

  const { data: categories = [] } = useQuery<Category[]>({
    queryKey: ['public-categories'],
    queryFn: listPublicCategories,
  })

  const { data: ctiTypes = [] } = useQuery<TicketType[]>({
    queryKey: ['public-types', ctiCategoryId || ticket?.category_id],
    queryFn: () => listPublicTypes(ctiCategoryId || ticket!.category_id),
    enabled: !!(ctiCategoryId || ticket?.category_id),
  })

  const activeCtiTypeId = ctiTypeId || ticket?.type_id || ''
  const { data: ctiItems = [] } = useQuery<TicketItem[]>({
    queryKey: ['public-items', ctiCategoryId || ticket?.category_id, activeCtiTypeId],
    queryFn: () => listPublicItems(ctiCategoryId || ticket!.category_id, activeCtiTypeId),
    enabled: !!activeCtiTypeId,
  })

  const ctiMutation = useMutation({
    mutationFn: () => updateTicket(id, {
      category_id: ctiCategoryId || ticket!.category_id,
      type_id: ctiTypeId || null,
      item_id: ctiItemId || null,
    }),
    onSuccess: () => {
      setCtiEdit(false)
      setCtiError('')
      qc.invalidateQueries({ queryKey: ['ticket', id] })
    },
    onError: (err) => setCtiError(extractError(err)),
  })

  function startCtiEdit() {
    setCtiCategoryId(ticket?.category_id ?? '')
    setCtiTypeId(ticket?.type_id ?? '')
    setCtiItemId(ticket?.item_id ?? '')
    setCtiError('')
    setCtiEdit(true)
  }

  const categoryName = categories.find((c) => c.id === ticket?.category_id)?.name
  const typeName = ctiTypes.find((t) => t.id === ticket?.type_id)?.name
  const itemName = ctiItems.find((i) => i.id === ticket?.item_id)?.name

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

  const { data: attachments = [] } = useQuery({
    queryKey: ['attachments', id],
    queryFn: () => listAttachments(id),
    enabled: !!ticket,
  })

  const statusName = statuses.find((s) => s.id === ticket?.status_id)?.name ?? '…'
  const statusColor = statuses.find((s) => s.id === ticket?.status_id)?.color

  const replyMutation = useMutation({
    mutationFn: () => addReply(id, replyBody, replyInternal, replyNotify),
    onSuccess: async () => {
      setReplyBody('')
      setReplyError('')
      qc.invalidateQueries({ queryKey: ['replies', id] })
      qc.invalidateQueries({ queryKey: ['statusHistory', id] })
      qc.invalidateQueries({ queryKey: ['ticket', id] })

      // Upload any attached files to the ticket.
      if (replyFiles.length > 0) {
        const initial: Record<string, UploadState> = {}
        for (const f of replyFiles) initial[f.name] = { status: 'pending' }
        setReplyUploadStates(initial)

        for (const f of replyFiles) {
          setReplyUploadStates((prev) => ({ ...prev!, [f.name]: { status: 'uploading' } }))
          try {
            await uploadAttachment(id, f)
            setReplyUploadStates((prev) => ({ ...prev!, [f.name]: { status: 'done' } }))
          } catch (err) {
            setReplyUploadStates((prev) => ({
              ...prev!,
              [f.name]: { status: 'error', error: extractError(err) },
            }))
          }
        }

        qc.invalidateQueries({ queryKey: ['attachments', id] })
        // Clear files after a short delay so the user can see the done states.
        setTimeout(() => {
          setReplyFiles([])
          setReplyUploadStates(undefined)
        }, 1500)
      }
    },
    onError: (err) => setReplyError(extractError(err)),
  })

  const resolveMutation = useMutation({
    mutationFn: () => resolveTicket(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ticket', id] })
      qc.invalidateQueries({ queryKey: ['statusHistory', id] })
    },
  })

  const reopenMutation = useMutation({
    mutationFn: () => reopenTicket(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ticket', id] })
      qc.invalidateQueries({ queryKey: ['statusHistory', id] })
    },
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

            {/* Timeline */}
            <div className="space-y-2">
              <h2 className="text-base font-semibold text-gray-900">Timeline</h2>
              {timeline.length === 0 && (
                <p className="text-sm text-gray-400">No activity yet.</p>
              )}
              {timeline.map((item) => {
                if (item.kind === 'reply') {
                  const r = item.data
                  return (
                    <div
                      key={r.id}
                      className={`rounded-lg border p-4 text-sm ${r.internal ? 'border-yellow-200 bg-yellow-50' : 'bg-white'}`}
                    >
                      <div className="mb-1 flex items-center justify-between text-xs text-gray-500">
                        <span>{r.author_id ?? 'Customer'}</span>
                        <span className="flex items-center gap-2">
                          {r.internal && <span className="text-yellow-600 font-medium">Internal note</span>}
                          {formatDate(r.created_at)}
                        </span>
                      </div>
                      <p className="whitespace-pre-wrap">{r.body}</p>
                    </div>
                  )
                }
                // Status history event
                const h = item.data
                return (
                  <div key={h.id} className="flex items-center gap-3 py-1 text-xs text-gray-400">
                    <div className="flex-1 border-t border-gray-100" />
                    <span className="shrink-0 text-center">
                      {h.from_status_id ? (
                        <>
                          <span style={{ color: h.from_status_color || undefined }} className="font-medium">
                            {h.from_status_name}
                          </span>
                          {' → '}
                          <span style={{ color: h.to_status_color || undefined }} className="font-medium">
                            {h.to_status_name}
                          </span>
                          {h.changed_by_name ? ` · ${h.changed_by_name}` : ' · System'}
                        </>
                      ) : (
                        <>
                          Ticket opened as{' '}
                          <span style={{ color: h.to_status_color || undefined }} className="font-medium">
                            {h.to_status_name}
                          </span>
                        </>
                      )}
                      {' · '}
                      {formatDate(h.created_at)}
                    </span>
                    <div className="flex-1 border-t border-gray-100" />
                  </div>
                )
              })}
            </div>

            {/* Reply / work log form */}
            {statusName !== 'Closed' && (
              <Card>
                <CardHeader>
                  <CardTitle className="text-sm">
                    {isStaffOrAdmin ? 'Add work log entry' : 'Add reply'}
                  </CardTitle>
                </CardHeader>
                <CardContent className="space-y-3">
                  <Textarea
                    placeholder={isStaffOrAdmin ? 'Describe the work performed or add a note…' : 'Type your reply…'}
                    rows={4}
                    value={replyBody}
                    onChange={(e) => setReplyBody(e.target.value)}
                    disabled={!!replyUploadStates}
                  />

                  {isStaffOrAdmin && (
                    <>
                      <div className="flex flex-wrap gap-4">
                        <label className="flex items-center gap-2 text-sm">
                          <input
                            type="checkbox"
                            checked={replyInternal}
                            onChange={(e) => {
                              setReplyInternal(e.target.checked)
                              if (e.target.checked) setReplyNotify(false)
                              else setReplyNotify(true)
                            }}
                            className="h-4 w-4 rounded border-gray-300"
                            disabled={!!replyUploadStates}
                          />
                          Internal note (not visible to customer)
                        </label>

                        {!replyInternal && (
                          <label className="flex items-center gap-2 text-sm">
                            <input
                              type="checkbox"
                              checked={replyNotify}
                              onChange={(e) => setReplyNotify(e.target.checked)}
                              className="h-4 w-4 rounded border-gray-300"
                              disabled={!!replyUploadStates}
                            />
                            Send ticket update email to customer
                          </label>
                        )}
                      </div>

                      <AttachmentUpload
                        files={replyFiles}
                        onChange={setReplyFiles}
                        uploadStates={replyUploadStates}
                        disabled={!!replyUploadStates}
                        maxFiles={5}
                      />
                    </>
                  )}

                  {replyError && <p className="text-sm text-red-600">{replyError}</p>}

                  <Button
                    onClick={() => replyMutation.mutate()}
                    disabled={replyMutation.isPending || !!replyUploadStates || !replyBody.trim()}
                  >
                    {replyMutation.isPending
                      ? 'Saving…'
                      : replyUploadStates
                      ? 'Uploading files…'
                      : isStaffOrAdmin
                      ? 'Save entry'
                      : 'Send reply'}
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

            <CustomFieldsPanel ticketId={id} isStaffOrAdmin={isStaffOrAdmin} />

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

            {attachments.length > 0 && (
              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
                    Attachments
                  </CardTitle>
                </CardHeader>
                <CardContent className="space-y-1">
                  {attachments.map((a) => (
                    <a
                      key={a.id}
                      href={attachmentDownloadUrl(id, a.id)}
                      className="flex items-center gap-2 text-sm text-blue-600 hover:underline truncate"
                      download={a.filename}
                    >
                      <span className="shrink-0 text-gray-400">↓</span>
                      <span className="truncate">{a.filename}</span>
                    </a>
                  ))}
                </CardContent>
              </Card>
            )}

            <Card>
              <CardHeader className="pb-2">
                <div className="flex items-center justify-between">
                  <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
                    Classification
                  </CardTitle>
                  {isStaffOrAdmin && !ctiEdit && (
                    <button className="text-xs text-blue-600 hover:underline" onClick={startCtiEdit}>
                      Edit
                    </button>
                  )}
                </div>
              </CardHeader>
              <CardContent className="space-y-2 text-sm">
                {ctiEdit ? (
                  <div className="space-y-2">
                    <div className="space-y-0.5">
                      <Label className="text-xs text-gray-500">Category</Label>
                      <Select
                        className="h-7 text-xs w-full"
                        value={ctiCategoryId}
                        onChange={(e) => { setCtiCategoryId(e.target.value); setCtiTypeId(''); setCtiItemId('') }}
                      >
                        <option value="">— select —</option>
                        {categories.filter((c) => c.active).map((c) => (
                          <option key={c.id} value={c.id}>{c.name}</option>
                        ))}
                      </Select>
                    </div>
                    {ctiTypes.length > 0 && (
                      <div className="space-y-0.5">
                        <Label className="text-xs text-gray-500">Type</Label>
                        <Select
                          className="h-7 text-xs w-full"
                          value={ctiTypeId}
                          onChange={(e) => { setCtiTypeId(e.target.value); setCtiItemId('') }}
                        >
                          <option value="">— none —</option>
                          {ctiTypes.filter((t) => t.active).map((t) => (
                            <option key={t.id} value={t.id}>{t.name}</option>
                          ))}
                        </Select>
                      </div>
                    )}
                    {ctiItems.length > 0 && (
                      <div className="space-y-0.5">
                        <Label className="text-xs text-gray-500">Item</Label>
                        <Select
                          className="h-7 text-xs w-full"
                          value={ctiItemId}
                          onChange={(e) => setCtiItemId(e.target.value)}
                        >
                          <option value="">— none —</option>
                          {ctiItems.filter((i) => i.active).map((i) => (
                            <option key={i.id} value={i.id}>{i.name}</option>
                          ))}
                        </Select>
                      </div>
                    )}
                    {ctiError && <p className="text-xs text-red-600">{ctiError}</p>}
                    <div className="flex gap-2 pt-1">
                      <Button
                        size="sm"
                        className="h-7 text-xs"
                        onClick={() => ctiMutation.mutate()}
                        disabled={ctiMutation.isPending || !ctiCategoryId}
                      >
                        {ctiMutation.isPending ? 'Saving…' : 'Save'}
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="h-7 text-xs"
                        onClick={() => { setCtiEdit(false); setCtiError('') }}
                      >
                        Cancel
                      </Button>
                    </div>
                  </div>
                ) : (
                  <>
                    <div className="flex justify-between">
                      <span className="text-gray-500">Category</span>
                      <span className="text-right text-xs font-medium">{categoryName ?? '—'}</span>
                    </div>
                    {ticket.type_id && (
                      <div className="flex justify-between">
                        <span className="text-gray-500">Type</span>
                        <span className="text-right text-xs">{typeName ?? '—'}</span>
                      </div>
                    )}
                    {ticket.item_id && (
                      <div className="flex justify-between">
                        <span className="text-gray-500">Item</span>
                        <span className="text-right text-xs">{itemName ?? '—'}</span>
                      </div>
                    )}
                  </>
                )}
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
