import { Link } from '@tanstack/react-router'
import { Layout } from '@/components/Layout'
import { Spinner } from '@/components/ui/spinner'
import { SettingsTabs, type SettingsTab } from '@/pages/admin/settings/SettingsTabs'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

/**
 * What every settings page has around its sections (#674): the layout, the h1
 * (the sidebar label), one line of description, the tab bar when the page has
 * tabs and, when the page has a draft, the save warning. While the draft is
 * not seeded the spinner stands in for the warning and the children, so no
 * section mounts on an empty draft.
 */
export function SettingsPageShell({ title, description, tabs, draft, wide, children }: {
  title: string
  description: string
  tabs?: SettingsTab[]
  draft?: SettingsDraft
  /** Without the max-w-2xl column: the template list and editor use the width. */
  wide?: boolean
  children: React.ReactNode
}) {
  const warning = draft?.warning
  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-fg">{title}</h1>
          <p className="mt-1 text-sm text-fg-faint">{description}</p>
        </div>

        {tabs && <SettingsTabs page={title} tabs={tabs} />}

        {draft?.isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <>
            {warning && (
              <div role="status" className="max-w-2xl rounded-md border border-warning-border bg-warning-soft px-4 py-3 text-sm text-warning-fg-strong">
                <p>{warning.text}</p>
                {warning.aboutClients && (
                  <p className="mt-1">
                    <Link to="/admin/oauth-clients" className="font-medium underline">
                      Open OAuth clients
                    </Link>{' '}
                    to see which clients have no groups.
                  </p>
                )}
              </div>
            )}
            <div className={wide ? undefined : 'max-w-2xl'}>{children}</div>
          </>
        )}
      </div>
    </Layout>
  )
}
