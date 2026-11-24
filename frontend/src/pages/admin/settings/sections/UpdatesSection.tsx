import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { UPDATE_CHECK_LABEL, UPDATE_CHECK_DETAIL } from '@/lib/updateCheck'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function UpdatesSection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool } = draft
  return (
    <Section title="Updates">
      <SettingRow label={UPDATE_CHECK_LABEL} description={UPDATE_CHECK_DETAIL}>
        <Toggle
          label={UPDATE_CHECK_LABEL}
          checked={bool('update_check_enabled')}
          onChange={(v) => setBool('update_check_enabled', v)}
        />
      </SettingRow>
    </Section>
  )
}
