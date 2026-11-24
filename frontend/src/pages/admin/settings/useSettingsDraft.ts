import { useEffect, useRef, useState } from 'react'
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

type Values = Record<string, unknown>

/* The draft is three layers, and a key's value is the first that has it:
     edits      what the operator last set, for the keys they changed;
     committed  what a save in flight carries, from the send until the settings
                have been fetched again (null when no save is in flight);
     the dump   the query's data.
   Nothing else is tracked: the draft, "dirty" and what a refetch keeps all
   follow from these. While a save is in flight (`inFlight`), a key it carries
   is held as an edit whatever its value, since the save may yet be refused and
   then the edit is all that is left of it.

   `committed` outlives the save in one case: the PATCH went through and the
   fresh settings could not be fetched. The dump is then older than what was
   saved, so `committed` stays under the edits as the base, and the next dump
   that arrives takes over. That keeps the form on what was saved, and "dirty"
   honest (it is judged against the base, not the stale dump). */
interface Layers {
  edits: Values
  committed: Values | null
  inFlight: boolean
}
const NO_LAYERS: Layers = { edits: {}, committed: null, inFlight: false }

// Equal by content, by JSON.stringify: an array set again with the same entries
// is the same value. The known looseness is harmless here: NaN equals null, and
// two objects with the same entries in another key order differ. A setting is a
// boolean, a number, a string or an array of strings.
function sameValue(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

// What is under the operator's edits: the save in flight if it carries the key,
// else the dump.
function baseValue(L: Layers, dump: Values | undefined, key: string): unknown {
  return L.committed && key in L.committed ? L.committed[key] : dump?.[key]
}

// Whether a save in flight carries the key: its edit is held, not judged.
function inFlightKey(L: Layers, key: string): boolean {
  return L.inFlight && L.committed !== null && key in L.committed
}

// The edits that are not just the value underneath them. An edit that equals
// its base is not a change, and must not hold the key against whoever changes
// it next. A key a save in flight carries is judged when the save settles.
function withoutNoopEdits(L: Layers, dump: Values | undefined): Layers {
  const edits: Values = {}
  for (const [k, v] of Object.entries(L.edits)) {
    if (inFlightKey(L, k) || !sameValue(v, baseValue(L, dump, k))) edits[k] = v
  }
  return Object.keys(edits).length === Object.keys(L.edits).length ? L : { ...L, edits }
}

// A string array as the dump holds it, or a comma list an older store wrote.
function asStrArr(v: unknown): string[] {
  if (Array.isArray(v)) return v as string[]
  if (typeof v === 'string' && v) return v.split(',').map((s) => s.trim()).filter(Boolean)
  return []
}

export function useSettingsDraft(): SettingsDraft {
  const qc = useQueryClient()
  const dumpNow = () => qc.getQueryData<Values>(SETTINGS_KEY)
  /* Seeded once the dump has been seen. A page opened with the dump already
     cached (from another settings page) is seeded in its first render. */
  const [seeded, setSeeded] = useState(() => dumpNow() !== undefined)
  const [layers, setLayers] = useState<Layers>(NO_LAYERS)
  // The same layers, for the handlers: two edits in one tick must each see the
  // other, and a callback of a save must see the layers as they are now, not as
  // they were when its render happened.
  const live = useRef<Layers>(NO_LAYERS)
  function putLayers(next: Layers) {
    if (next === live.current) return
    live.current = next
    setLayers(next)
  }

  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)
  // What the server said about a save that went through (the PATCH answers 200
  // with a warning instead of 204). Kept until the next save, not timed out:
  // it is something to act on, such as OAuth clients left seeing no tickets.
  const [warning, setWarning] = useState<{ text: string; aboutClients: boolean } | null>(null)

  /* The reputation API keys are write-only, so none of them is ever in the
     draft and none travels with an ordinary save. One is sent only when the
     operator typed it or asked for the stored one to be removed — three blank
     fields PATCHed as "" would wipe three working keys on an unrelated change,
     and nothing would report it: the lookups would simply stop.

     Keyed by settings key rather than held as one value, which is the whole
     point: a shared field would paste one service's key into another's
     account. */
  const [secrets, setSecrets] = useState<Record<string, string>>({})
  const [cleared, setCleared] = useState<Record<string, boolean>>({})

  const query = useQuery({ queryKey: SETTINGS_KEY, queryFn: getSettings })
  const settings = query.data

  /* When the dump changes, an edit that now equals it is no longer a change
     (someone else made it); drop it, so that nothing stays held. This runs
     setState from an effect, the cascading-render pattern
     react-hooks/set-state-in-effect warns about
     (https://react.dev/learn/you-might-not-need-an-effect). Suppressed, not
     refactored: switching the lint gate on should not also change how the admin
     forms behave. */
  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    if (!settings) return
    setSeeded(true)
    const L = live.current
    // A dump that arrives while no save is in flight is newer than a save that
    // went through without its fetch; it takes over from `committed`.
    putLayers(withoutNoopEdits(L.inFlight ? L : { ...L, committed: null }, settings))
  }, [settings, query.dataUpdatedAt]) // a fetch that changed nothing is a dump too
  /* eslint-enable react-hooks/set-state-in-effect */

  // The composed draft, as an object for the accessors and the patch.
  const draft: Values = { ...settings, ...layers.committed, ...layers.edits }

  // When the PATCH answered: a fetch of the settings that landed after it has
  // the saved values in it.
  const answeredAt = useRef(0)

  const saveMutation = useMutation({
    mutationFn: async () => {
      /* The dump's "<key>_set" entries are the server describing secrets it
         will not send, not settings. Sending one back writes a settings row by
         that name which nothing ever reads. */
      // Only what the operator changed (#394), with the guest clamp (#673); see
      // buildSettingsPatch.
      const L = live.current
      // Against what is under the edits: after a save whose fetch failed, that
      // is not the dump.
      const base = { ...dumpNow(), ...L.committed }
      const patch = buildSettingsPatch(base, { ...base, ...L.edits }, secrets, cleared)
      // What the save carries, as the operator set it (the patch can hold a
      // clamped figure instead). It is what the draft shows until the settings
      // have been fetched again.
      const committed: Values = { ...L.committed }
      for (const k of Object.keys(patch)) if (k in L.edits) committed[k] = L.edits[k]
      putLayers({ ...L, committed, inFlight: true })
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
      // What was sent is saved. A key the operator has set again since keeps its edit.
      const L = live.current
      const edits = { ...L.edits }
      for (const [k, v] of Object.entries(L.committed ?? {})) {
        if (k in edits && sameValue(edits[k], v)) delete edits[k]
      }
      putLayers({ ...L, edits })
      answeredAt.current = Date.now()
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
    // After the fresh settings have been asked for, or the save was refused:
    // nothing is in flight, and the edits that remain are the ones still unsaved.
    onSettled: (_data, error) => {
      const L = live.current
      let next: Layers
      if (error) {
        // Refused: what was sent is still the operator's, and unsaved.
        next = { edits: { ...L.committed, ...L.edits }, committed: null, inFlight: false }
      } else if ((qc.getQueryState(SETTINGS_KEY)?.dataUpdatedAt ?? 0) >= answeredAt.current) {
        // The fresh settings have the saved values in them.
        next = { ...L, committed: null, inFlight: false }
      } else {
        // The fetch failed: keep showing what was saved until a dump arrives.
        next = { ...L, inFlight: false }
      }
      putLayers(withoutNoopEdits(next, dumpNow()))
    },
    onError: (err) =>
      setSaveError(
        // The server's text names the raw setting; say what an admin can do.
        extractErrorCode(err) === 'email_required'
          ? 'Email is not set up, so signup and guest submission cannot be turned on: both send an email the person needs. Set SMTP_HOST and SMTP_FROM in docker/.env, restart the server, then try again.'
          : extractError(err),
      ),
  })

  // The operator sets a key. It is an edit unless it is the value underneath
  // it: the save in flight if that carries the key, else the dump. So a key put
  // back to what the dump holds is not held, and one put back to its pre-save
  // value while the save is on its way is, since the save is about to change it.
  function set(key: string, v: unknown) {
    const L = live.current
    const edits = { ...L.edits }
    if (!inFlightKey(L, key) && sameValue(v, baseValue(L, dumpNow(), key))) delete edits[key]
    else edits[key] = v
    putLayers({ ...L, edits })
  }

  const dirty = !saveMutation.isPending
    && Object.keys(buildSettingsPatch({ ...settings, ...layers.committed }, draft, secrets, cleared)).length > 0

  return {
    isLoading: query.isLoading || (settings !== undefined && !seeded),
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
      // From the value the draft holds now, whichever layer it comes from.
      const L = live.current
      const now = key in L.edits ? L.edits[key] : baseValue(L, dumpNow(), key)
      const current = asStrArr(now)
      const next = checked ? [...new Set([...current, item])] : current.filter((x) => x !== item)
      set(key, next)
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
