import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listAdminAudit, listAssignableStaff, type AdminAuditFilters } from '@/api/admin'
import { useAuthStore } from '@/store/auth'
import { Layout } from '@/components/Layout'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { AuditDiff } from '@/components/AuditDiff'
import { followRemovalLabel, isFollowAction } from '@/lib/followAudit'
import { isReplyAction, replyActionLabel } from '@/lib/replyAudit'
import { EyeOffIcon } from 'lucide-react'
import type { AdminAuditEntry } from '@/api/types'

const PAGE_SIZE = 50

// Known entity types an audit entry can name today (audit.Entry.EntityType's
// own comment: "ticket", "user", "group", etc.) — a starting set, not an
// enum the server enforces. The filter still accepts any string typed
// directly; this is only what the dropdown offers.
const ENTITY_TYPES = ['ticket', 'user', 'group', 'sla_policy', 'category']

function labelFor(action: string): string {
  return action.replace(/_/g, ' ').replace(/^./, (c) => c.toUpperCase())
}

// Local-datetime-input value ("2026-01-01T12:00") to the RFC3339 the API
// filter wants, in this browser's own offset — an admin picking "today at
// 9am" means their 9am, not UTC's.
function toRFC3339(local: string): string | undefined {
  if (!local) return undefined
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

interface FilterState {
  entityType: string
  action: string
  actorId: string
  from: string
  to: string
  q: string
  hideReplies: boolean
}

const EMPTY_FILTERS: FilterState = { entityType: '', action: '', actorId: '', from: '', to: '', q: '', hideReplies: false }

// #129's admin-wide half: searchable across every entity, not just one
// ticket — see handleListAdminAudit. Staff reach this page too (the nav
// link and the route are both staff+admin), scoped and diff-gated entirely
// server-side; this page renders whatever it is sent and does not attempt
// to re-derive who should see what.
export function AdminAuditPage() {
  const role = useAuthStore((s) => s.user?.role)
  const [filters, setFilters] = useState<FilterState>(EMPTY_FILTERS)
  const [offset, setOffset] = useState(0)

  const apiFilters: AdminAuditFilters = {
    entity_type: filters.entityType || undefined,
    action: filters.action.trim() || undefined,
    actor_id: filters.actorId || undefined,
    from: toRFC3339(filters.from),
    to: toRFC3339(filters.to),
    q: filters.q.trim() || undefined,
    exclude_action: filters.hideReplies ? ['reply_added', 'note_added'] : undefined,
    limit: PAGE_SIZE,
    offset,
  }

  const { data, isLoading, isError } = useQuery({
    queryKey: ['adminAudit', apiFilters],
    queryFn: () => listAdminAudit(apiFilters),
  })

  const { data: staff = [] } = useQuery({
    queryKey: ['assignableStaff'],
    queryFn: listAssignableStaff,
  })

  function updateFilter<K extends keyof FilterState>(key: K, value: FilterState[K]) {
    setFilters((f) => ({ ...f, [key]: value }))
    setOffset(0)
  }

  const entries = data?.entries ?? []
  const total = data?.total ?? 0
  // The server stops counting at a cap and says so; a capped total is a floor,
  // so it reads "10,000+". An uncapped one is exact, even when it equals the cap.
  const fmt = (n: number) => n.toLocaleString('en-US')
  const totalLabel = `${fmt(total)}${data?.total_capped ? '+' : ''}`
  // The server says whether another page exists; the total is for display.
  const hasNextPage = data?.has_more ?? false

  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Audit Log</h1>
          <p className="mt-1 text-sm text-gray-500">
            {role === 'staff'
              ? 'Ticket changes within your own scope.'
              : 'Every recorded change, across tickets, accounts and settings.'}
          </p>
        </div>

        <div className="grid grid-cols-2 gap-4 rounded-lg border bg-white p-4 md:grid-cols-3 lg:grid-cols-6">
          {/* Staff are forced to entity_type=ticket server-side regardless of
              this control, so it is only offered to admin — a staff member
              picking "user" here would see an empty page for a reason the
              page gives no way to discover. */}
          {role === 'admin' && (
            <div className="space-y-1">
              <Label htmlFor="audit-entity-type">Entity type</Label>
              <Select
                id="audit-entity-type"
                value={filters.entityType}
                onChange={(e) => updateFilter('entityType', e.target.value)}
              >
                <option value="">All</option>
                {ENTITY_TYPES.map((t) => (
                  <option key={t} value={t}>{t}</option>
                ))}
              </Select>
            </div>
          )}
          <div className="space-y-1">
            <Label htmlFor="audit-action">Action</Label>
            <Input
              id="audit-action"
              value={filters.action}
              onChange={(e) => updateFilter('action', e.target.value)}
              placeholder="resolved"
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="audit-actor">Actor</Label>
            <Select
              id="audit-actor"
              value={filters.actorId}
              onChange={(e) => updateFilter('actorId', e.target.value)}
            >
              <option value="">Anyone</option>
              {staff.map((s) => (
                <option key={s.id} value={s.id}>{s.display_name}</option>
              ))}
            </Select>
          </div>
          <div className="space-y-1">
            <Label htmlFor="audit-from">From</Label>
            <Input
              id="audit-from" type="datetime-local"
              value={filters.from}
              onChange={(e) => updateFilter('from', e.target.value)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="audit-to">To</Label>
            <Input
              id="audit-to" type="datetime-local"
              value={filters.to}
              onChange={(e) => updateFilter('to', e.target.value)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="audit-q">Search</Label>
            <Input
              id="audit-q"
              value={filters.q}
              onChange={(e) => updateFilter('q', e.target.value)}
              placeholder="entity or action"
            />
          </div>
          <div className="flex items-center gap-2 col-span-full">
            <input
              id="audit-hide-replies"
              type="checkbox"
              checked={filters.hideReplies}
              onChange={(e) => updateFilter('hideReplies', e.target.checked)}
            />
            <Label htmlFor="audit-hide-replies">Hide replies and notes</Label>
          </div>
        </div>

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : isError ? (
          <p role="alert" className="text-sm text-red-600">Could not load the audit log.</p>
        ) : (
          <>
            <div className="rounded-lg border bg-white overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="bg-gray-50 text-xs text-gray-500 uppercase">
                  <tr>
                    <th className="px-4 py-3 text-left">When</th>
                    <th className="px-4 py-3 text-left">Entity</th>
                    <th className="px-4 py-3 text-left">Action</th>
                    <th className="px-4 py-3 text-left">Actor</th>
                  </tr>
                </thead>
                <tbody className="divide-y">
                  {entries.map((e: AdminAuditEntry) => (
                    <tr key={e.id} className="align-top">
                      <td className="whitespace-nowrap px-4 py-3 text-gray-500">
                        {new Date(e.created_at).toLocaleString()}
                      </td>
                      <td className="px-4 py-3 text-gray-600">
                        <span className="font-mono text-xs">{e.entity_type}</span>
                      </td>
                      <td className="px-4 py-3">
                        <span className="font-medium text-gray-900">{isReplyAction(e.action) ? replyActionLabel(e.action) : (followRemovalLabel(e) ?? labelFor(e.action))}</span>
                        {/* A follow entry's diff only repeats what its label says. */}
                        {!isFollowAction(e.action) && !isReplyAction(e.action) && <AuditDiff before={e.before} after={e.after} names={e.names} />}
                        {e.entity_type === 'email_template' && e.before && !e.after && <RemovedTemplateText before={e.before} />}
                      </td>
                      <td className="px-4 py-3 text-gray-600 wrap-anywhere">
                        {e.actor_masked
                          ? <span className="italic text-gray-500"><EyeOffIcon aria-hidden="true" className="mr-1 inline h-3.5 w-3.5" />Requester (name hidden)</span>
                          : (e.actor_name || (isReplyAction(e.action) ? (e.actor_id ? 'Someone' : 'A guest') : '—'))}
                      </td>
                    </tr>
                  ))}
                  {entries.length === 0 && (
                    <tr>
                      <td colSpan={4} className="px-4 py-8 text-center text-gray-400">
                        Nothing matches these filters.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>

            <div className="flex items-center justify-between">
              <p className="text-sm text-gray-500">
                {entries.length === 0
                  ? ''
                  : `${fmt(offset + 1)}–${fmt(offset + entries.length)} of ${totalLabel}`}
              </p>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  disabled={offset === 0}
                  onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
                >
                  Previous
                </Button>
                <Button
                  variant="outline"
                  disabled={!hasNextPage}
                  onClick={() => setOffset((o) => o + PAGE_SIZE)}
                >
                  Next
                </Button>
              </div>
            </div>
          </>
        )}
      </div>
    </Layout>
  )
}

// #158: reverting an email template deletes the edited text, and the revert
// dialog promises it stays in the audit log. A revert has no "after", which
// AuditDiff leaves out on purpose for every other entity type, so this entity
// type alone shows what was removed.
function RemovedTemplateText({ before }: { before: Record<string, unknown> }) {
  const subject = typeof before.template_subject === 'string' ? before.template_subject : ''
  const body = typeof before.template_body === 'string' ? before.template_body : ''
  return (
    <div className="mt-1 border-l-2 border-gray-100 pl-2 text-gray-500">
      <p>Removed text:</p>
      {subject && <p className="wrap-anywhere"><span className="font-mono">template_subject</span>: {subject}</p>}
      <pre className="whitespace-pre-wrap font-mono text-xs [overflow-wrap:anywhere]">{body}</pre>
    </div>
  )
}
