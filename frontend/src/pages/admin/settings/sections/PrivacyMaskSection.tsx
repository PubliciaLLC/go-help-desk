import { Section } from '@/components/admin/Section'
import { SettingRow } from '@/pages/admin/settings/primitives'
import { Select } from '@/components/ui/select'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function PrivacyMaskSection({ draft }: { draft: SettingsDraft }) {
  const { str, setStr } = draft
  return (
    <Section title="Privacy">
      <SettingRow
        label="Mask requester names in Audit Trail"
        description="Where an audit view shows a requester (the person who filed the ticket) as 'Requester (name hidden)' instead of by name. Staff and admin names are always shown: the audit trail exists to say which employee did what. Whether someone counts as a requester depends on their role now, not on their role when the entry was written. This hides names from the list only: it is not an access control. A masked entry still records who acted, so anyone who can see it can still identify the requester by opening the ticket, and an administrator can also open the user it records. Everywhere (the default) masks both the admin-wide audit log and each ticket's Activity feed. The mask also applies to searching the audit log (a requester is not found by name or email) and to the ticket's status timeline, and to the names inside a change. Someone with direct access to the database can still read every name. Each change to this setting is recorded in the audit log. The ticket page itself still shows the requester's name either way."
      >
        <Select
          className="w-52"
          aria-label="Mask requester names in Audit Trail"
          value={str('audit_mask_requester_names') || 'everywhere'}
          onChange={(e) => setStr('audit_mask_requester_names', e.target.value)}
        >
          <option value="admin_log">Admin Audit Log Only</option>
          <option value="ticket_log">Ticket Audit Log</option>
          <option value="everywhere">Everywhere</option>
        </Select>
      </SettingRow>
    </Section>
  )
}
