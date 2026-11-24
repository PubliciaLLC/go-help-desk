import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { SLAPoliciesSection } from '@/pages/admin/settings/sections/SLAPoliciesSection'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function SLASection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool } = draft
  return (
    <Section title="SLA">
      <div>
        <SettingRow
          label="SLA tracking"
          description="Track response and resolution targets per priority and category. A policy is attached when a ticket is created, a background sweep marks a target breached when it reaches or passes it, and the queue shows each ticket's standing. This toggle is the switch; SLA_ENABLED in the environment turns it on at every start."
        >
          <Toggle checked={bool('sla_enabled')} onChange={(v) => setBool('sla_enabled', v)} />
        </SettingRow>
        {bool('sla_enabled') && <SLAPoliciesSection />}
      </div>
    </Section>
  )
}
