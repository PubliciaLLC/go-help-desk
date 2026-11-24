import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function SLAAlertsSection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool } = draft
  return (
    <Section title="SLA alerts">
      <SettingRow
        label="SLA alerts"
        description="Tell staff by email, and send to webhooks that subscribe to them, when a ticket's response or resolution target is breached or has reached 80% of its time. Each person's SLA alerts switch and the Email override still decide who is mailed. Off on an install upgraded from 1.2, so nothing is sent until you turn it on; a breach that happened while this was off is not announced later."
      >
        <Toggle label="SLA alerts" checked={bool('sla_alerts_enabled')} onChange={(v) => setBool('sla_alerts_enabled', v)} />
      </SettingRow>
    </Section>
  )
}
