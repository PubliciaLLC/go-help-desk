import { SaveBar } from '@/pages/admin/settings/primitives'
import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { EMAIL_DESCRIPTION, EMAIL_TABS, EMAIL_TITLE } from '@/pages/admin/settings/emailTabs'
import { useLeaveGuard } from '@/pages/admin/settings/useLeaveGuard'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { StaffEmailSection } from '@/pages/admin/settings/sections/StaffEmailSection'
import { SLAAlertsSection } from '@/pages/admin/settings/sections/SLAAlertsSection'

// System -> Email -> Notifications (#674). Not routed until plan task 7. The
// Templates tab is its own page with no draft, so leaving for it with unsaved
// edits asks first, like any other link out of this page.
export function EmailSettingsPage() {
  const draft = useSettingsDraft()
  const guard = useLeaveGuard(draft)
  return (
    <SettingsPageShell title={EMAIL_TITLE} description={EMAIL_DESCRIPTION} tabs={EMAIL_TABS} draft={draft}>
      <div className="space-y-6">
        <StaffEmailSection draft={draft} />
        <SLAAlertsSection draft={draft} />
        <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
      </div>
      {guard}
    </SettingsPageShell>
  )
}
