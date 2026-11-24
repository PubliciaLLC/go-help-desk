import { SaveBar } from '@/pages/admin/settings/primitives'
import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { useLeaveGuard } from '@/pages/admin/settings/useLeaveGuard'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { UploadsSection } from '@/pages/admin/settings/sections/UploadsSection'
import { AttachmentSizeSection } from '@/pages/admin/settings/sections/AttachmentSizeSection'
import { MalwareScanningSection } from '@/pages/admin/settings/sections/MalwareScanningSection'
import { ReputationSection } from '@/pages/admin/settings/sections/ReputationSection'

// System -> File Attachments (#674). Not routed until plan task 7.
export function AttachmentSettingsPage() {
  const draft = useSettingsDraft()
  const guard = useLeaveGuard(draft)
  return (
    <SettingsPageShell
      title="File Attachments"
      description="What can be uploaded, how large it may be, and how uploads are checked."
      draft={draft}
    >
      <div className="space-y-6">
        <UploadsSection draft={draft} />
        <AttachmentSizeSection draft={draft} />
        <MalwareScanningSection draft={draft} />
        <ReputationSection draft={draft} />
        <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
      </div>
      {guard}
    </SettingsPageShell>
  )
}
