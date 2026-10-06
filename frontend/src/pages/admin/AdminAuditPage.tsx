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
}

const EMPTY_FILTERS: FilterState = { entityType: '', action: '', actorId: '', from: '', to: '', q: '' }

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
  // null for staff, who are given no count — see AdminAuditListResponse.total.
  const total = data?.total ?? null
  // Never derived from total: for staff there is no total, and the count that
  // used to be there was the server's own pre-scope figure, so paging off it
  // promised pages that did not exist and skipped entries that did.
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
        </div>

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : isError ? (
          <p role="alert" className="text-sm text-red-600">Could not load the audit log.</p>
        ) : (
          <>
            <div className="rounded-lg border bg-white overflow-hidden">
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
                        <span className="font-medium text-gray-900">{labelFor(e.action)}</span>
                        <AuditDiff before={e.before} after={e.after} />
                      </td>
                      <td className="px-4 py-3 text-gray-600">{e.actor_name || '—'}</td>
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
                  : total !== null
                    ? `${offset + 1}–${offset + entries.length} of ${total}`
                    : `${offset + 1}–${offset + entries.length}`}
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
