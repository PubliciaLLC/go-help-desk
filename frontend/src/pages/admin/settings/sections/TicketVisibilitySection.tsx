import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { AvailableQueueSection } from '@/components/admin/AvailableQueueSection'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function TicketVisibilitySection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool } = draft
  return (
    <Section title="Ticket visibility">
      <SettingRow
        label="Limit staff to their group scope"
        description="When on, a staff member sees only tickets they reported, tickets assigned to them or to one of their groups, and tickets whose category and type a group of theirs covers. Admins always see everything. Off by default: with it off, every staff member can see every ticket, which is how earlier versions behaved. Turn it on once your groups and their category scopes are configured — staff in no group will see almost nothing."
      >
        <Toggle
          checked={bool('ticket_scope_enforced')}
          onChange={(v) => setBool('ticket_scope_enforced', v)}
        />
      </SettingRow>
      <AvailableQueueSection />
    </Section>
  )
}
