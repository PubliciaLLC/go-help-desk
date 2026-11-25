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
        label="Users may choose the Item"
        description="Show signed-in requesters an Item selector under Type on the new-ticket page, with the same description and custom fields staff get. Off by default: requesters choose a Category and a Type, and staff assign the Item afterwards."
      >
        <Toggle
          label="Users may choose the Item"
          checked={bool('users_can_choose_item')}
          onChange={(v) => setBool('users_can_choose_item', v)}
        />
      </SettingRow>
    </Section>
  )
}
