import { SaveBar } from '@/pages/admin/settings/primitives'
import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { useLeaveGuard } from '@/pages/admin/settings/useLeaveGuard'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { SubmissionsSection } from '@/pages/admin/settings/sections/SubmissionsSection'

// System -> Guest Access (#674). Not routed until plan task 7. The guest size
// slider's top is the per-file limit, which nothing on this page edits, so the
// draft's value is the stored one and the section needs no page-specific code
// (attachmentLimits, with the reader's fallback of 25).
export function GuestAccessSettingsPage() {
  const draft = useSettingsDraft()
  const guard = useLeaveGuard(draft)
  return (
    <SettingsPageShell
      title="Guest Access"
      description="Tickets from people without an account."
      draft={draft}
    >
      <div className="space-y-6">
        <SubmissionsSection draft={draft} />
        <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
      </div>
      {guard}
    </SettingsPageShell>
  )
}
