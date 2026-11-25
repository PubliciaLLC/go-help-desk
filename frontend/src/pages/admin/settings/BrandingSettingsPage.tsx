import { SaveBar } from '@/pages/admin/settings/primitives'
import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { useLeaveGuard } from '@/pages/admin/settings/useLeaveGuard'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { IdentitySection } from '@/pages/admin/settings/sections/IdentitySection'
import { IconSection } from '@/pages/admin/settings/sections/IconSection'

// System -> Branding (#674). The draft holds the site name and the home screen
// name (site_short_name). The logo uploader and the icon section keep their own
// state and act at once, so the save bar does not cover them; the icon section
// follows it.
export function BrandingSettingsPage() {
  const draft = useSettingsDraft()
  const guard = useLeaveGuard(draft)
  return (
    <SettingsPageShell
      title="Branding"
      description="The name, logo and icon in the sidebar, the browser tab and the home screen."
      draft={draft}
    >
      <div className="space-y-6">
        <IdentitySection draft={draft} />
        <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
        <IconSection />
      </div>
      {guard}
    </SettingsPageShell>
  )
}
