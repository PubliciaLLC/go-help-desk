import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { LifetimeSlider } from '@/components/admin/LifetimeSlider'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function APIKeysSettingsSection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool, has, num, setNum } = draft
  return (
    <Section title="API keys">
      <SettingRow
        label="Staff may create their own API keys"
        description="When on, staff can create their own API keys under Account. Turning it off only stops staff creating new keys: keys that already exist keep working, and staff can still list and revoke their own. Administrators can always create keys, and can issue one on behalf of a staff member from Admin → API Keys. Requesters never own keys. A key acts as its owner and can carry only permissions that owner's role has. Off by default."
      >
        <Toggle
          label="Staff may create their own API keys"
          checked={bool('staff_can_create_api_keys')}
          onChange={(v) => setBool('staff_can_create_api_keys', v)}
        />
      </SettingRow>
      <div className="border-t px-5 py-4 space-y-2">
        <div className="text-sm text-fg-faint">
          Every new API key must expire, and no later than this. It is checked when a key is created, so lowering it
          does not break keys that already exist; they are marked in the key lists so they can be rotated. Choosing
          Never lets keys be made with no expiry.
        </div>
        <LifetimeSlider
          value={has('api_key_max_lifetime_years') ? num('api_key_max_lifetime_years') : undefined}
          onChange={(v) => setNum('api_key_max_lifetime_years', v)}
        />
      </div>
    </Section>
  )
}
