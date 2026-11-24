import { SaveBar } from '@/pages/admin/settings/primitives'
import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { useLeaveGuard } from '@/pages/admin/settings/useLeaveGuard'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { IdentitySection } from '@/pages/admin/settings/sections/IdentitySection'

// System -> Branding (#674). The draft holds only
// the site name; the logo uploader keeps its own state.
export function BrandingSettingsPage() {
  const draft = useSettingsDraft()
  const guard = useLeaveGuard(draft)
  return (
    <SettingsPageShell
      title="Branding"
      description="The name and logo in the sidebar and the browser tab."
      draft={draft}
    >
      <div className="space-y-6">
        <IdentitySection draft={draft} />
        <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
      </div>
      {guard}
    </SettingsPageShell>
  )
}
