import { SaveBar } from '@/pages/admin/settings/primitives'
import { SettingsPageShell } from '@/pages/admin/settings/SettingsPageShell'
import { useLeaveGuard } from '@/pages/admin/settings/useLeaveGuard'
import { useSettingsDraft } from '@/pages/admin/settings/useSettingsDraft'
import { TicketsSection } from '@/pages/admin/settings/sections/TicketsSection'
import { TicketLifecycleSection } from '@/pages/admin/settings/sections/TicketLifecycleSection'
import { UpdatesSection } from '@/pages/admin/settings/sections/UpdatesSection'

// System -> General (#674).
export function GeneralSettingsPage() {
  const draft = useSettingsDraft()
  const guard = useLeaveGuard(draft)
  return (
    <SettingsPageShell
      title="General"
      description="Ticket numbers, the ticket lifecycle and update checks."
      draft={draft}
    >
      <div className="space-y-6">
        <TicketsSection draft={draft} />
        <TicketLifecycleSection draft={draft} />
        <UpdatesSection draft={draft} />
        <SaveBar onSave={draft.save} isPending={draft.isPending} error={draft.error} saved={draft.saved} />
      </div>
      {guard}
    </SettingsPageShell>
  )
}
