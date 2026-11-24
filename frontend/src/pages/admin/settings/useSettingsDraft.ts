import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings } from '@/api/admin'
import { extractError, extractErrorCode } from '@/api/client'
import { buildSettingsPatch } from '@/pages/admin/settings/buildSettingsPatch'

const SETTINGS_KEY = ['admin', 'settings']

/** One settings page's draft: what the operator has edited, and its save (#674). */
export interface SettingsDraft {
  /** No seeded draft yet: the read is in flight, or its data has arrived but
   *  has not been copied into the draft. A page shows its spinner meanwhile,
   *  so no section mounts on an empty draft. */
  isLoading: boolean
  /** The dump as last fetched. */
  stored: Record<string, unknown> | undefined
  bool(k: string): boolean
  num(k: string): number
  str(k: string): string
  strArr(k: string): string[]
  has(k: string): boolean
  setBool(k: string, v: boolean): void
  setNum(k: string, v: number): void
  setStr(k: string, v: string): void
  setStrArr(k: string, v: string[]): void
  toggleStrArr(k: string, item: string, checked: boolean): void
  /** Write-only keys (today: the three reputation API keys). Never in the draft. */
  secret(k: string): string
  setSecret(k: string, v: string): void
  secretCleared(k: string): boolean
  setSecretCleared(k: string, v: boolean): void
  /** The patch a save would send is not empty. */
  dirty: boolean
  save(): void
  isPending: boolean
  error: string
  saved: boolean
  /** What the server said about a save that went through. */
  warning: { text: string; aboutClients: boolean } | null
}

// A string array as the dump holds it, or a comma list an older store wrote.
function asStrArr(v: unknown): string[] {
  if (Array.isArray(v)) return v as string[]
  if (typeof v === 'string' && v) return v.split(',').map((s) => s.trim()).filter(Boolean)
  return []
}

export function useSettingsDraft(): SettingsDraft {
  const qc = useQueryClient()
  /* null until seeded. A page opened with the dump already cached (from another
     settings page) is seeded in its first render. */
  const [local, setLocal] = useState<Record<string, unknown> | null>(
    () => qc.getQueryData<Record<string, unknown>>(SETTINGS_KEY) ?? null,
  )
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)
  // What the server said about a save that went through (the PATCH answers 200
  // with a warning instead of 204). Kept until the next save, not timed out:
  // it is something to act on, such as OAuth clients left seeing no tickets.
  const [warning, setWarning] = useState<{ text: string; aboutClients: boolean } | null>(null)

  /* The reputation API keys are write-only, so none of them is ever in `local`
     and none travels with an ordinary save. One is sent only when the operator
     typed it or asked for the stored one to be removed — three blank fields
     PATCHed as "" would wipe three working keys on an unrelated change, and
     nothing would report it: the lookups would simply stop.

     Keyed by settings key rather than held as one value, which is the whole
     point: a shared field would paste one service's key into another's
     account. */
  const [secrets, setSecrets] = useState<Record<string, string>>({})
  const [cleared, setCleared] = useState<Record<string, boolean>>({})

  const query = useQuery({ queryKey: SETTINGS_KEY, queryFn: getSettings })
  const settings = query.data

  /* The effects below seed local form state from a react-query result. That is
     the cascading-render pattern react-hooks/set-state-in-effect warns about
     (https://react.dev/learn/you-might-not-need-an-effect). Suppressed here,
     not refactored: switching the lint gate on should not also change how the
     admin forms behave. TODO: derive the state, or remount the form with a key.
     eslint-disable-next-line is not enough — the rule reports at each setState
     call, so the whole effect is wrapped. */
  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    if (settings) setLocal(settings)
  }, [settings])
  /* eslint-enable react-hooks/set-state-in-effect */

  const draft = local ?? {}

  const saveMutation = useMutation({
    mutationFn: async () => {
      /* The dump's "<key>_set" entries are the server describing secrets it
         will not send, not settings. Sending one back writes a settings row by
         that name which nothing ever reads. */
      // Only what the operator changed (#394), with the guest clamp (#673); see
      // buildSettingsPatch.
      const patch = buildSettingsPatch(settings, draft, secrets, cleared)
      const res = await updateSettings(patch)
      // The save that turned ticket scope on is the one whose warning is about
      // OAuth clients; an SSO warning shares the field and gets no such link.
      return { ...res, aboutClients: patch.ticket_scope_enforced === true }
    },
    onSuccess: async (res) => {
      setSaved(true)
      setSaveError('')
      setWarning(res.warning ? { text: res.warning, aboutClients: res.aboutClients } : null)
      // Only on success: a refused save leaves every edit where it was,
      // including a key typed but not yet accepted.
      setSecrets({})
      setCleared({})
      setTimeout(() => setSaved(false), 2500)
      qc.invalidateQueries({ queryKey: ['site-config'] })
      // The scan policy and address feed the banner and the scanner status;
      // without this a saved "Do not scan" keeps warning for five minutes.
      qc.invalidateQueries({ queryKey: ['security-warnings'] })
      // The ticket list reads sla_enabled from /me.
      qc.invalidateQueries({ queryKey: ['me'] })
      // Awaited: the save stays pending until the fresh dump has arrived, so the
      // draft never differs from the old dump because of a change already saved.
      await qc.invalidateQueries({ queryKey: SETTINGS_KEY })
    },
    onError: (err) =>
      setSaveError(
        // The server's text names the raw setting; say what an admin can do.
        extractErrorCode(err) === 'email_required'
          ? 'Email is not set up, so signup and guest submission cannot be turned on: both send an email the person needs. Set SMTP_HOST and SMTP_FROM in docker/.env, restart the server, then try again.'
          : extractError(err),
      ),
  })

  function set(key: string, v: unknown) { setLocal((s) => ({ ...s, [key]: v })) }

  const dirty = !saveMutation.isPending
    && Object.keys(buildSettingsPatch(settings, draft, secrets, cleared)).length > 0

  return {
    isLoading: query.isLoading || (settings !== undefined && local === null),
    stored: settings,
    bool: (key) => Boolean(draft[key]),
    num: (key) => Number(draft[key] ?? 0),
    str: (key) => String(draft[key] ?? ''),
    strArr: (key) => asStrArr(draft[key]),
    // Whether this instance has ever stored a value for the key, as opposed to
    // what the value is. The difference matters where an unset setting does not
    // mean the zero value — see the allowed attachment types.
    has: (key) => key in draft,
    setBool: set,
    setNum: set,
    setStr: set,
    setStrArr: set,
    toggleStrArr: (key, item, checked) => {
      setLocal((s) => {
        const current = asStrArr((s ?? {})[key])
        const next = checked ? [...new Set([...current, item])] : current.filter((x) => x !== item)
        return { ...s, [key]: next }
      })
    },
    secret: (key) => secrets[key] ?? '',
    setSecret: (key, v) => setSecrets((s) => ({ ...s, [key]: v })),
    secretCleared: (key) => Boolean(cleared[key]),
    setSecretCleared: (key, v) => setCleared((s) => ({ ...s, [key]: v })),
    dirty,
    save: () => saveMutation.mutate(),
    isPending: saveMutation.isPending,
    error: saveError,
    saved,
    warning,
  }
}
