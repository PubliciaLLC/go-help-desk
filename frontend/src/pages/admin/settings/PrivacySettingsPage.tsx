import { SaveBar } from '@/pages/admin/settings/primitives'
import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { useLeaveGuard } from '@/pages/admin/settings/useLeaveGuard'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { TicketVisibilitySection } from '@/pages/admin/settings/sections/TicketVisibilitySection'
import { AvailableQueueSection } from '@/components/admin/AvailableQueueSection'
import { AuditLogSettingsSection } from '@/pages/admin/settings/sections/AuditLogSettingsSection'
import { PrivacyMaskSection } from '@/pages/admin/settings/sections/PrivacyMaskSection'

// System -> Privacy (#674). Available queue has
// its own save and is not part of the draft.
export function PrivacySettingsPage() {
  const draft = useSettingsDraft()
  const guard = useLeaveGuard(draft)
  return (
    <SettingsPageShell
      title="Privacy"
      description="Who sees which tickets, and what the audit trail shows and keeps."
      draft={draft}
    >
      <div className="space-y-6">
        <TicketVisibilitySection draft={draft} />
        <AvailableQueueSection />
        <AuditLogSettingsSection draft={draft} />
        <PrivacyMaskSection draft={draft} />
        <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
      </div>
      {guard}
    </SettingsPageShell>
  )
}
