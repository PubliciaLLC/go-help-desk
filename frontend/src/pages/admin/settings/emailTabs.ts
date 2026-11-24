import type { SettingsTab } from '@/pages/admin/settings/SettingsTabs'

// The two addresses of System -> Email (#674). Both pages show the same title,
// description and tabs, so they are written once.
export const EMAIL_TITLE = 'Email'
export const EMAIL_DESCRIPTION =
  'The email staff receive about tickets, and the wording of every email this help desk sends.'
export const EMAIL_TABS: SettingsTab[] = [
  { label: 'Notifications', to: '/admin/settings/email' },
  { label: 'Templates', to: '/admin/settings/email/templates' },
]
