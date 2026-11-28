import { SaveBar } from '@/pages/admin/settings/primitives'
import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { useLeaveGuard } from '@/pages/admin/settings/useLeaveGuard'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { SLASection } from '@/pages/admin/settings/sections/SLASection'

const TABS = [{ label: 'SLA', to: '/admin/settings/features' }]

// System -> Features (#674).
export function FeaturesSettingsPage() {
  const draft = useSettingsDraft()
  const guard = useLeaveGuard(draft)
  return (
    <SettingsPageShell
      title="Features"
      description="Optional features of this help desk."
      tabs={TABS}
      draft={draft}
    >
      <div className="space-y-6">
        <SLASection draft={draft} />
        <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
      </div>
      {guard}
    </SettingsPageShell>
  )
}
