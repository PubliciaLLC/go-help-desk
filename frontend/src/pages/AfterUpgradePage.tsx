import { useEffect, useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getPostUpgrade, getSecurityWarnings, getSettings, updateSettings } from '@/api/admin'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { LifetimeSlider } from '@/components/admin/LifetimeSlider'
import { UPDATE_CHECK_DETAIL, UPDATE_CHECK_LABEL } from '@/lib/updateCheck'

// The one-time "after upgrading to 1.3.0" wizard, shown to the first
// administrator who signs in on an install that was upgraded (see
// lib/afterUpgrade.ts and the dashboard guard in router.tsx).
//
// Every value goes through the ordinary PATCH /admin/settings, so the server's
// validation and its session-only rules apply unchanged. Next saves what the
// step changed; Skip saves nothing and drops that step's edits, so the
// setting keeps whatever it was. Finishing, or skipping the last step, writes
// post_upgrade_130_seen in the same request, and the wizard never shows again.
// Closing the tab part-way leaves it unseen: it comes back, with the steps
// already saved showing their new values.

type Settings = Record<string, unknown>

interface StepProps {
  settings: Settings
  /** Edits not yet saved. */
  draft: Settings
  /** The step's current value: the unsaved edit if there is one, else the stored one. */
  get: (key: string) => unknown
  set: (key: string, value: unknown) => void
  unset: (...keys: string[]) => void
  extra: { oldPasswordAccounts?: number; emailEnabled?: boolean }
}

interface Step {
  title: string
  keys: string[]
  Body: (p: StepProps) => React.ReactElement
}

// ── Step 1: weekly update check ──────────────────────────────────────────────
function UpdateCheckStep({ settings, get, set }: StepProps) {
  const on = get('update_check_enabled') === true
  return (
    <div className="space-y-3">
      <p className="text-sm text-gray-700">
        New in 1.3.0. This server can check for new releases of Go Help Desk, so that an out-of-date install does not go
        unnoticed. It does nothing unless you turn it on here.
      </p>
      <p className="text-sm text-gray-500">{UPDATE_CHECK_DETAIL}</p>
      <p className="text-sm text-gray-700">Currently: {settings.update_check_enabled === true ? 'on' : 'off'}.</p>
      <div className="flex items-start gap-2">
        <input
          id="wiz-update-check"
          type="checkbox"
          className="mt-0.5 h-4 w-4 shrink-0 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
          checked={on}
          onChange={(e) => set('update_check_enabled', e.target.checked)}
        />
        <label htmlFor="wiz-update-check" className="text-sm font-medium text-gray-900">
          {UPDATE_CHECK_LABEL}
        </label>
      </div>
    </div>
  )
}

// ── Step 2: staff email ──────────────────────────────────────────────────────
const STAFF_EMAIL_KEYS = [
  'staff_email_assigned_override',
  'staff_email_activity_override',
  'staff_email_group_override',
]
const OVERRIDE_TEXT: Record<string, string> = {
  user: 'Each person chooses',
  on: 'On for everyone',
  off: 'Off for everyone',
}

function StaffEmailStep({ settings, draft, set, unset }: StepProps) {
  const stored = STAFF_EMAIL_KEYS.map((k) => (typeof settings[k] === 'string' ? (settings[k] as string) : 'user'))
  const same = stored.every((v) => v === stored[0])
  // The select shows "keep" until this step is edited, then the edited value.
  // Reading the draft rather than the stored value is the point: a stored
  // "off" must not make "Off for everyone" look already chosen.
  const choice = typeof draft[STAFF_EMAIL_KEYS[0]] === 'string' ? (draft[STAFF_EMAIL_KEYS[0]] as string) : 'keep'

  function pick(v: string) {
    if (v === 'keep') unset(...STAFF_EMAIL_KEYS)
    else for (const k of STAFF_EMAIL_KEYS) set(k, v)
  }

  return (
    <div className="space-y-3">
      <p className="text-sm text-gray-700">
        Staff can be emailed when a ticket is assigned to them or their group, and when a requester replies on a ticket
        that is theirs. Upgrading from 1.2 turned them off, so that no staff email started without you deciding it. Email
        only has an effect once it is set up on the server.
      </p>
      <p className="text-sm text-gray-700">
        Currently: {same ? OVERRIDE_TEXT[stored[0]] ?? stored[0] : 'a mix of settings'}.
      </p>
      <Select aria-label="Staff email notifications" value={choice} onChange={(e) => pick(e.target.value)}>
        <option value="keep">Leave as it is</option>
        <option value="user">Let each person choose</option>
        <option value="on">On for everyone</option>
        <option value="off">Off for everyone</option>
      </Select>
    </div>
  )
}

// ── Step 3: staff API keys ───────────────────────────────────────────────────
function ApiKeysStep({ settings, get, set }: StepProps) {
  const years = typeof settings.api_key_max_lifetime_years === 'number' ? settings.api_key_max_lifetime_years : 2
  return (
    <div className="space-y-3">
      <p className="text-sm text-gray-700">
        New in 1.3.0: staff can create their own API keys when you allow it. A key acts as its owner and can carry only
        what that owner&rsquo;s role may do. Every new key must expire within the longest lifetime set here (2 years
        unless you change it); keys that already exist are not affected.
      </p>
      <p className="text-sm text-gray-700">
        Currently: staff {settings.staff_can_create_api_keys === true ? 'can' : 'cannot'} create keys; new keys may live
        up to {years === -1 ? 'any length of time' : years === 1 ? '1 year' : `${years} years`}.
      </p>
      <div className="flex items-start gap-2">
        <input
          id="wiz-staff-keys"
          type="checkbox"
          className="mt-0.5 h-4 w-4 shrink-0 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
          checked={get('staff_can_create_api_keys') === true}
          onChange={(e) => set('staff_can_create_api_keys', e.target.checked)}
        />
        <label htmlFor="wiz-staff-keys" className="text-sm font-medium text-gray-900">
          Staff may create their own API keys
        </label>
      </div>
      <LifetimeSlider
        value={typeof get('api_key_max_lifetime_years') === 'number' ? (get('api_key_max_lifetime_years') as number) : undefined}
        onChange={(v) => set('api_key_max_lifetime_years', v)}
      />
    </div>
  )
}

// ── Step 4: old-password reset window ────────────────────────────────────────
function PasswordsStep({ settings, get, set, extra }: StepProps) {
  const stored = typeof settings.password_rehash_grace_days === 'number' ? settings.password_rehash_grace_days : 30
  const days = typeof get('password_rehash_grace_days') === 'number' ? (get('password_rehash_grace_days') as number) : stored
  const n = extra.oldPasswordAccounts
  return (
    <div className="space-y-3">
      <p className="text-sm text-gray-700">
        1.3.0 saves passwords in a stronger form. An account is upgraded the next time its owner signs in. Once the window
        below has passed since 1.3.0 first started, anyone whose password is still in the old form must reset it before
        signing in. Administrators are exempt, so the instance cannot lock itself out. The default is 30 days.
      </p>
      <p className="text-sm text-gray-700">Currently: {stored} days.</p>
      {n !== undefined && (
        <p className="text-sm text-gray-700">
          {n === 0
            ? 'No enabled account is still on an old password.'
            : `${n} ${n === 1 ? 'account is' : 'accounts are'} still on an old password.`}{' '}
          {n > 0 && extra.emailEnabled === true && 'Email is set up, so reset links can be emailed to them.'}
          {n > 0 && extra.emailEnabled === false &&
            'Email is not set up, so reset links cannot be emailed; an administrator would have to set their passwords from Admin, Users.'}
        </p>
      )}
      <div className="flex items-center gap-2">
        <Input
          type="number" min={1} max={365} className="w-24 text-right"
          aria-label="Days before an old password must be reset"
          value={days}
          onChange={(e) => set('password_rehash_grace_days', Math.min(365, Math.max(0, parseInt(e.target.value, 10) || 0)))}
          onBlur={() => { if (days < 1) set('password_rehash_grace_days', 1) }}
        />
        <span className="text-sm text-gray-500">days</span>
      </div>
    </div>
  )
}

// To add a step, add one entry. Guest file uploads (#536) goes after the
// password step once that PR has merged: allow guest uploads, plus size,
// per-ticket and per-hour limits, reusing #536's controls.
// STEP 5 GOES HERE.
const STEPS: Step[] = [
  { title: 'Weekly check for new releases', keys: ['update_check_enabled'], Body: UpdateCheckStep },
  { title: 'Staff email notifications', keys: STAFF_EMAIL_KEYS, Body: StaffEmailStep },
  { title: 'Staff API keys', keys: ['staff_can_create_api_keys', 'api_key_max_lifetime_years'], Body: ApiKeysStep },
  { title: 'Reset window for old passwords', keys: ['password_rehash_grace_days'], Body: PasswordsStep },
]

export function AfterUpgradePage() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [index, setIndex] = useState(0)
  const [draft, setDraft] = useState<Settings>({})
  const [error, setError] = useState('')
  const headingRef = useRef<HTMLHeadingElement>(null)

  const { data: settings } = useQuery({ queryKey: ['admin', 'settings'], queryFn: getSettings })
  // Neither of these is needed to answer a step; a failure just leaves its note out.
  const { data: warnings } = useQuery({ queryKey: ['security-warnings'], queryFn: getSecurityWarnings, retry: false })
  const { data: upgrade } = useQuery({ queryKey: ['admin', 'post-upgrade'], queryFn: getPostUpgrade, retry: false })

  const save = useMutation({ mutationFn: (patch: Settings) => updateSettings(patch) })

  // Move focus to the step's heading, so a screen reader announces the change.
  useEffect(() => {
    headingRef.current?.focus()
  }, [index, settings])

  if (!settings) {
    return <div className="p-6 text-sm text-gray-500">Loading…</div>
  }

  const step = STEPS[index]
  const last = index === STEPS.length - 1

  const get = (key: string) => (key in draft ? draft[key] : settings[key])
  const set = (key: string, value: unknown) => setDraft((d) => ({ ...d, [key]: value }))
  const unset = (...keys: string[]) =>
    setDraft((d) => Object.fromEntries(Object.entries(d).filter(([k]) => !keys.includes(k))))

  async function move(saveStep: boolean) {
    setError('')
    const patch: Settings = {}
    if (saveStep) {
      for (const k of step.keys) if (k in draft) patch[k] = draft[k]
      if ('password_rehash_grace_days' in patch && (patch.password_rehash_grace_days as number) < 1) {
        patch.password_rehash_grace_days = 1
      }
    } else {
      unset(...step.keys)
    }
    if (last) patch.post_upgrade_130_seen = true

    if (Object.keys(patch).length > 0) {
      try {
        await save.mutateAsync(patch)
      } catch (err) {
        setError(extractError(err))
        return
      }
      await qc.invalidateQueries({ queryKey: ['admin', 'settings'] })
    }
    if (last) navigate({ to: '/dashboard' })
    else setIndex(index + 1)
  }

  return (
    <div className="flex min-h-dvh items-start justify-center bg-gray-50 p-4">
      <Card className="w-full max-w-lg">
        <CardHeader>
          <h1 className="text-xl font-semibold tracking-tight">After upgrading to 1.3.0</h1>
          <p className="mt-1 text-sm text-gray-500">
            A few things changed. Each step shows what your install does today. Next saves a change; Skip leaves the
            setting as it is. You will not be asked again.
          </p>
        </CardHeader>
        <CardContent className="space-y-4">
          <p aria-live="polite" className="text-xs font-medium uppercase tracking-wider text-gray-400">
            Step {index + 1} of {STEPS.length}
          </p>
          <h2 ref={headingRef} tabIndex={-1} className="text-lg font-semibold text-gray-900 focus:outline-none">
            {step.title}
          </h2>
          <step.Body
            settings={settings}
            draft={draft}
            get={get}
            set={set}
            unset={unset}
            extra={{ oldPasswordAccounts: upgrade?.old_password_accounts, emailEnabled: warnings?.email?.enabled }}
          />
          {error && <p role="alert" className="break-words text-sm text-red-600">{error}</p>}
          <div className="flex flex-wrap items-center justify-between gap-2 pt-2">
            <Button type="button" variant="outline" disabled={index === 0 || save.isPending} onClick={() => { setError(''); setIndex(index - 1) }}>
              Back
            </Button>
            <div className="flex flex-wrap gap-2">
              <Button type="button" variant="outline" disabled={save.isPending} onClick={() => move(false)}>
                Skip
              </Button>
              <Button type="button" disabled={save.isPending} onClick={() => move(true)}>
                {last ? 'Finish' : 'Next'}
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
