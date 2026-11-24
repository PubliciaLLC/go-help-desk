import { SaveBar } from '@/pages/admin/settings/primitives'
import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { useLeaveGuard } from '@/pages/admin/settings/useLeaveGuard'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { SAMLSettingsSection } from '@/pages/admin/settings/sections/SAMLSettingsSection'
import { OIDCSettingsSection } from '@/pages/admin/settings/sections/OIDCSettingsSection'
import { MFASection } from '@/pages/admin/settings/sections/MFASection'
import { PasswordsSection } from '@/pages/admin/settings/sections/PasswordsSection'
import { APIKeysSettingsSection } from '@/pages/admin/settings/sections/APIKeysSettingsSection'
import { RegistrationSection } from '@/pages/admin/settings/sections/RegistrationSection'

// System -> Authentication (#674).
export function AuthenticationSettingsPage() {
  const draft = useSettingsDraft()
  const guard = useLeaveGuard(draft)
  return (
    <SettingsPageShell
      title="Authentication"
      description="How people sign in, and who may create API keys."
      draft={draft}
    >
      <div className="space-y-6">
        <SAMLSettingsSection draft={draft} />
        <OIDCSettingsSection />
        <MFASection draft={draft} />
        <PasswordsSection draft={draft} />
        <APIKeysSettingsSection draft={draft} />
        <RegistrationSection draft={draft} />
        <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
      </div>
      {guard}
    </SettingsPageShell>
  )
}
