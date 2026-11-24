import { Link } from '@tanstack/react-router'
import { Layout } from '@/components/Layout'
import { Spinner } from '@/components/ui/spinner'
import { cn } from '@/lib/utils'
import { useState } from 'react'
import { SaveBar } from '@/pages/admin/settings/primitives'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { TicketsSection } from '@/pages/admin/settings/sections/TicketsSection'
import { SubmissionsSection } from '@/pages/admin/settings/sections/SubmissionsSection'
import { TicketLifecycleSection } from '@/pages/admin/settings/sections/TicketLifecycleSection'
import { TicketVisibilitySection } from '@/pages/admin/settings/sections/TicketVisibilitySection'
import { AvailableQueueSection } from '@/components/admin/AvailableQueueSection'
import { AuditLogSettingsSection } from '@/pages/admin/settings/sections/AuditLogSettingsSection'
import { UpdatesSection } from '@/pages/admin/settings/sections/UpdatesSection'
import { PrivacyMaskSection } from '@/pages/admin/settings/sections/PrivacyMaskSection'
import { IdentitySection } from '@/pages/admin/settings/sections/IdentitySection'
import { SAMLSettingsSection } from '@/pages/admin/settings/sections/SAMLSettingsSection'
import { OIDCSettingsSection } from '@/pages/admin/settings/sections/OIDCSettingsSection'
import { MFASection } from '@/pages/admin/settings/sections/MFASection'
import { PasswordsSection } from '@/pages/admin/settings/sections/PasswordsSection'
import { APIKeysSettingsSection } from '@/pages/admin/settings/sections/APIKeysSettingsSection'
import { RegistrationSection } from '@/pages/admin/settings/sections/RegistrationSection'
import { SLASection } from '@/pages/admin/settings/sections/SLASection'
import { SLAAlertsSection } from '@/pages/admin/settings/sections/SLAAlertsSection'
import { EmailTemplatesSection } from '@/pages/admin/settings/sections/EmailTemplatesSection'
import { StaffEmailSection } from '@/pages/admin/settings/sections/StaffEmailSection'
import { UploadsSection } from '@/pages/admin/settings/sections/UploadsSection'
import { AttachmentSizeSection } from '@/pages/admin/settings/sections/AttachmentSizeSection'
import { MalwareScanningSection } from '@/pages/admin/settings/sections/MalwareScanningSection'
import { ReputationSection } from '@/pages/admin/settings/sections/ReputationSection'

// ── Tab definitions ───────────────────────────────────────────────────────────

type Tab = 'general' | 'branding' | 'auth' | 'features' | 'email' | 'attachments'

const TABS: { id: Tab; label: string }[] = [
  { id: 'general',     label: 'General' },
  { id: 'branding',    label: 'Branding' },
  { id: 'auth',        label: 'Authentication' },
  { id: 'features',    label: 'Features' },
  { id: 'email',       label: 'Email' },
  { id: 'attachments', label: 'Attachments' },
]

// ── Tab panels ────────────────────────────────────────────────────────────────

// Each tab is its list of sections, in the order the tab shows them. Every
// section takes the page's draft (#674); sections that read nothing from it
// take none.
type Part = (props: { draft: SettingsDraft }) => React.JSX.Element

const PANELS: Record<Tab, Part[]> = {
  general: [
    TicketsSection,
    SubmissionsSection,
    TicketLifecycleSection,
    TicketVisibilitySection,
    AvailableQueueSection,
    AuditLogSettingsSection,
    UpdatesSection,
    PrivacyMaskSection,
  ],
  branding: [IdentitySection],
  auth: [
    SAMLSettingsSection,
    OIDCSettingsSection,
    MFASection,
    PasswordsSection,
    APIKeysSettingsSection,
    RegistrationSection,
  ],
  features: [SLASection, SLAAlertsSection],
  email: [EmailTemplatesSection, StaffEmailSection],
  attachments: [UploadsSection, AttachmentSizeSection, MalwareScanningSection, ReputationSection],
}

// ── Page ──────────────────────────────────────────────────────────────────────

export function SettingsPage() {
  const [activeTab, setActiveTab] = useState<Tab>('general')
  const draft = useSettingsDraft()
  const saveWarning = draft.warning

  if (draft.isLoading) {
    return <Layout><div className="flex justify-center py-12"><Spinner /></div></Layout>
  }

  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Settings</h1>
          <p className="mt-1 text-sm text-gray-500">System-wide configuration for this instance.</p>
        </div>

        {/* Tab bar */}
        <div className="border-b">
          <nav className="-mb-px flex gap-6 overflow-x-auto">
            {TABS.map((tab) => (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id)}
                className={cn(
                  'border-b-2 pb-3 text-sm font-medium whitespace-nowrap transition-colors',
                  activeTab === tab.id
                    ? 'border-blue-600 text-blue-600'
                    : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700'
                )}
              >
                {tab.label}
              </button>
            ))}
          </nav>
        </div>

        {saveWarning && (
          <div role="status" className="max-w-2xl rounded-md border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900">
            <p>{saveWarning.text}</p>
            {saveWarning.aboutClients && (
              <p className="mt-1">
                <Link to="/admin/oauth-clients" className="font-medium underline">
                  Open OAuth clients
                </Link>{' '}
                to see which clients have no groups.
              </p>
            )}
          </div>
        )}

        {/* Active panel */}
        <div className="max-w-2xl">
          <div className="space-y-6">
            {PANELS[activeTab].map((Part, i) => <Part key={i} draft={draft} />)}
            <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
          </div>
        </div>
      </div>
    </Layout>
  )
}
