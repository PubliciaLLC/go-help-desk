import { Section } from '@/components/admin/Section'
import { SettingRow } from '@/pages/admin/settings/primitives'
import { Input } from '@/components/ui/input'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function PasswordsSection({ draft }: { draft: SettingsDraft }) {
  const { has, num, setNum } = draft
  return (
    <Section title="Passwords">
      <SettingRow
        label="Reset window for old passwords"
        description="Releases before 1.3.0 saved passwords in a weaker form. An account is upgraded the next time its owner signs in. Once this many days have passed since 1.3.0 first started, anyone whose password is still in the old form must reset it before they can sign in. Administrators are exempt, so the instance cannot lock itself out. New installs are not affected."
      >
        <div className="flex items-center gap-2">
          <Input
            type="number" min={1} max={365} className="w-24 text-right"
            aria-label="Days before an old password must be reset"
            // 30 until the operator edits it, because 30 is what the server
            // uses when nothing is stored. The upper clamp keeps a typo from
            // being a value the server refuses. The lower clamp (1) waits for
            // the operator to leave the field: clamping on every keystroke
            // would re-inject a minimum between digits when the field is
            // cleared to retype (see the audit retention field).
            value={has('password_rehash_grace_days') ? num('password_rehash_grace_days') : 30}
            onChange={(e) => setNum('password_rehash_grace_days', Math.min(365, Math.max(0, parseInt(e.target.value, 10) || 0)))}
            onBlur={() => {
              if (has('password_rehash_grace_days') && num('password_rehash_grace_days') < 1) setNum('password_rehash_grace_days', 1)
            }}
          />
          <span className="text-sm text-fg-faint">days</span>
        </div>
      </SettingRow>
    </Section>
  )
}
