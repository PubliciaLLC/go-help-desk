import { Section } from '@/components/admin/Section'
import { SettingRow } from '@/pages/admin/settings/primitives'
import { useQuery } from '@tanstack/react-query'
import { listStatuses } from '@/api/admin'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function TicketLifecycleSection({ draft }: { draft: SettingsDraft }) {
  const { num, setNum, str, setStr } = draft
  const { data: statuses = [] } = useQuery({ queryKey: ['statuses'], queryFn: listStatuses })
  // Reopen target should be an active, non-system status (not Resolved/Closed).
  const targetableStatuses = statuses.filter((s) => s.active && s.kind !== 'system')
  return (
    <Section title="Ticket lifecycle">
      <SettingRow
        label="Reopen window and auto-close"
        description="How many days after resolution a requester may reopen their ticket by adding a reply. This is also when the ticket closes: once the window ends, a Resolved ticket is closed automatically, and a closed ticket is read-only for everyone who filed it and cannot be reopened by a requester (staff and admins open a linked follow-up ticket instead, or reopen it if you allow that below). Set to 0 for no reopening at all: a Resolved ticket is closed and read-only on the next automatic sweep, about 5 minutes later."
      >
        <div className="flex items-center gap-2">
          <Input
            type="number" min={0} className="w-20 text-right"
            value={num('reopen_window_days')}
            onChange={(e) => setNum('reopen_window_days', Math.max(0, parseInt(e.target.value, 10) || 0))}
          />
          <span className="text-sm text-fg-faint">days</span>
        </div>
      </SettingRow>
      <SettingRow
        label="Reopening closed tickets"
        description="Whether a Closed ticket can be reopened at all, and by whom. Off (the default): a closed ticket is archived read-only and nobody can reopen it; staff and admins open a linked follow-up ticket instead. Admins only: an administrator may force-reopen a closed ticket. Staff and admins: either may. Requesters (users and guests) can never reopen a closed ticket, whatever this is set to, and a follow-up is always available. Takes effect on the next request."
      >
        <Select
          className="w-44"
          aria-label="Reopening closed tickets"
          value={str('closed_reopen_policy') || 'off'}
          onChange={(e) => setStr('closed_reopen_policy', e.target.value)}
        >
          <option value="off">Off (terminal)</option>
          <option value="admin">Admins only</option>
          <option value="staff_admin">Staff and admins</option>
        </Select>
      </SettingRow>
      <SettingRow
        label="Reopen target status"
        description="The status a ticket is moved to when it is reopened: a Resolved ticket by its requester's reply, or a Closed one by a force-reopen (when allowed above)."
      >
        <Select
          className="w-44"
          value={str('reopen_target_status_name')}
          onChange={(e) => setStr('reopen_target_status_name', e.target.value)}
        >
          <option value="">Select…</option>
          {targetableStatuses.map((s) => (
            <option key={s.id} value={s.name}>{s.name}</option>
          ))}
        </Select>
      </SettingRow>
    </Section>
  )
}
