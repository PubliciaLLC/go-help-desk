import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { Input } from '@/components/ui/input'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function TicketsSection({ draft }: { draft: SettingsDraft }) {
  const { str, setStr, bool, setBool } = draft
  return (
    <Section title="Tickets">
      <SettingRow
        label="Tracking number prefix"
        description="The GHD in GHD-2026-000001. One to eight uppercase letters or digits. Changing it affects only tickets created afterwards — numbers already issued are never rewritten, because they are in customers' inboxes and referenced in their replies. An instance that predates the rename from Open Help Desk can set this to OHD to keep one consistent series."
      >
        <Input
          value={str('ticket_prefix') || 'GHD'}
          onChange={(e) => setStr('ticket_prefix', e.target.value.toUpperCase())}
          maxLength={8}
          className="w-32 font-mono"
          aria-label="Tracking number prefix"
        />
      </SettingRow>
      {/* Off unless saved (#711): an absent row reads off, here and on the server. */}
      <SettingRow
        label="Show Items to registered users"
        description="Show signed-in requesters the Item under each Type: an Item selector on the new-ticket page and the Item on their tickets, with the same description and custom fields staff get. Off by default: requesters see Category and Type only, and neither the Item nor the values of fields only an Item supplies are sent to them (not on their tickets, not in lists or filters, not over MCP), and staff assign it afterwards."
      >
        <Toggle
          label="Show Items to registered users"
          checked={bool('users_show_items')}
          onChange={(v) => setBool('users_show_items', v)}
        />
      </SettingRow>
    </Section>
  )
}
