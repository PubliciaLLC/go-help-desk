import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { Input } from '@/components/ui/input'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function AuditLogSettingsSection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool, num, setNum, has } = draft
  return (
    <Section title="Audit log">
      <SettingRow
        label="Staff can view ticket change history"
        description="When on, staff also see the field-level before/after detail on a ticket's Activity feed — not just what happened and who did it, which staff already see either way. Admins always see this detail; reporters never do, regardless of this setting. Off by default: earlier versions shipped without this detail visible to staff at all."
      >
        <Toggle
          label="Staff can view ticket change history"
          checked={bool('staff_can_view_ticket_change_history')}
          onChange={(v) => setBool('staff_can_view_ticket_change_history', v)}
        />
      </SettingRow>
      <SettingRow
        label="Staff can view follow history"
        description="When on, staff also see the entries recording who started and stopped following a ticket, on its Activity feed. Admins always see them; reporters never do. Staff always see the current list of followers on a ticket, whatever this is set to. Off by default."
      >
        <Toggle
          label="Staff can view follow history"
          checked={bool('staff_can_view_follow_audit')}
          onChange={(v) => setBool('staff_can_view_follow_audit', v)}
        />
      </SettingRow>
      <SettingRow
        label="Retention"
        description="How many days an audit entry is kept before it is permanently deleted. Zero keeps everything forever, which is the default and what every release before 1.3.0 did. Applies to every entry system-wide — ticket changes, account changes, everything the audit log records."
      >
        <div className="flex items-center gap-2">
          <Input
            type="number" min={0} max={36525} className="w-24 text-right"
            aria-label="Retention, in days"
            // Shows 0 when unset, because 0 is what the server does when
            // unset: keep forever. It used to show 365 here while the server
            // kept everything, telling the operator they had a one-year
            // window they did not have — the same "believes they have a
            // window and has an unbounded table" failure the settings
            // handler refuses a bad PATCH to avoid, delivered by the UI.
            //
            // The fallback applies only until the operator's first edit, not
            // on every keystroke: a fallback baked into onChange re-injected
            // the default between digits, so typing "30" produced 36530.
            // has() flips true on that first edit. The server treats any
            // non-positive value as forever and refuses anything above
            // AuditRetentionMaxDays, so this field stays usable rather than
            // enforcing.
            value={has('audit_retention_days') ? num('audit_retention_days') : 0}
            onChange={(e) => setNum('audit_retention_days', Math.max(0, parseInt(e.target.value, 10) || 0))}
          />
          <span className="text-sm text-fg-faint">
            {(has('audit_retention_days') ? num('audit_retention_days') : 0) > 0
              ? 'days'
              : 'days — 0 keeps everything'}
          </span>
        </div>
      </SettingRow>
    </Section>
  )
}
