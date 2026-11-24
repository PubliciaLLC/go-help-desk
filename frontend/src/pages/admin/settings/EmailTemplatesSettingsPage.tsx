import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { EMAIL_DESCRIPTION, EMAIL_TABS, EMAIL_TITLE } from '@/pages/admin/settings/emailTabs'
import { EmailTemplatesTab } from '@/pages/admin/settings/EmailTemplatesTab'

// System -> Email -> Templates (#674). Not routed until plan task 7. No draft,
// no leave guard and no save bar: each template saves itself.
export function EmailTemplatesSettingsPage() {
  return (
    <SettingsPageShell title={EMAIL_TITLE} description={EMAIL_DESCRIPTION} tabs={EMAIL_TABS} wide>
      <EmailTemplatesTab />
    </SettingsPageShell>
  )
}
