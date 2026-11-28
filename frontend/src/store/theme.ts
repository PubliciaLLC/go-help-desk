import { create } from 'zustand'
import { updateAppearance } from '@/api/preferences'
import type { ThemePreference } from '@/api/types'
import {
  applyTheme,
  initialPreference,
  readStored,
  resolve,
  systemPrefersDark,
  writeStored,
  type ResolvedTheme,
} from '@/lib/theme'
import { useAuthStore } from './auth'

// Which theme the page uses (#709). The precedence rules, in one place:
//
//  - Signed in: the account's value wins (adoptServer) and is mirrored to
//    localStorage, so the login page looks the same next time.
//  - Not signed in: whatever this browser last stored, else "device".
//  - Signing out changes nothing here.
//  - A choice applies at once; when signed in it is also saved, and put back
//    if the save fails.
//
// ThemeController wires the auth store, the storage event and the media query
// to the actions below. Nothing else writes `preference`.

export const SAVE_ERROR = "Couldn't save your appearance. Try again."
const ERROR_FOR_MS = 5000

interface ThemeState {
  /** What the person chose: light, dark, or follow the device. */
  preference: ThemePreference
  /** The browser's prefers-color-scheme, kept so `device` resolves reactively. */
  prefersDark: boolean
  /** Saves in flight (a second click while one is out sends a second request). */
  pending: number
  saving: boolean
  error: string | null
  /** Counts successful saves; the switch shows "Saved." for a moment after it moves. */
  saveCount: number
  /** Labelled switches on the page (Your account). They announce errors; the icon switches then stay quiet. */
  labelledSwitches: number
  trackLabelled: (delta: 1 | -1) => void
  /** The person's choice, from a switch. Applies, remembers, and saves when signed in. */
  setPreference: (next: ThemePreference) => Promise<void>
  /** The account's value, from /me or a sign-in answer. Absent (an older server) changes nothing. */
  adoptServer: (theme: ThemePreference | undefined) => void
  /** Another tab changed the stored value: follow it, without writing it back or sending it. */
  adoptLocal: (p: ThemePreference) => void
  setSystemDark: (dark: boolean) => void
}

export const useThemeStore = create<ThemeState>((set, get) => {
  // Saves are numbered as they are sent. The answer of an older save that comes
  // back after a newer one has been answered says nothing the account does not
  // already know, so it must not put the older theme back in the auth store.
  let sent = 0
  let confirmed = 0

  /** Sets the preference and puts the resolved theme on the page. */
  function show(preference: ThemePreference) {
    set({ preference })
    applyTheme(resolve(preference, get().prefersDark))
  }

  return {
    preference: initialPreference(readStored()),
    prefersDark: systemPrefersDark(),
    pending: 0,
    saving: false,
    error: null,
    saveCount: 0,
    labelledSwitches: 0,
    trackLabelled: (delta) => set((s) => ({ labelledSwitches: s.labelledSwitches + delta })),

    async setPreference(next) {
      const prev = get().preference
      const seq = ++sent
      set({ error: null })
      show(next)
      writeStored(next)
      // Not signed in: there is no account to save to.
      if (!useAuthStore.getState().user) return

      set((s) => ({ pending: s.pending + 1, saving: true }))
      try {
        const saved = await updateAppearance(next)
        // The auth store now holds the saved theme. adoptServer ignores this
        // update: a save is still counted as pending until the line below. Not
        // for a person who has signed out since, nor for an answer older than
        // one already taken.
        if (seq > confirmed && useAuthStore.getState().user?.id === saved.id) {
          confirmed = seq
          useAuthStore.getState().setUser(saved)
        }
        set((s) => ({ error: null, saveCount: s.saveCount + 1 }))
      } catch {
        // Put back what the server last confirmed (never a value that was only
        // clicked, as `prev` may be), unless a newer choice has been made since:
        // that one has its own save and says what the person wants.
        if (get().preference === next) {
          const back = useAuthStore.getState().user?.theme ?? prev
          show(back)
          writeStored(back)
          set({ error: SAVE_ERROR })
          // A message that stays until the next choice would sit there for good.
          setTimeout(() => {
            if (get().error === SAVE_ERROR) set({ error: null })
          }, ERROR_FOR_MS)
        }
      } finally {
        set((s) => ({ pending: s.pending - 1, saving: s.pending - 1 > 0 }))
      }
    },

    adoptServer(theme) {
      if (theme === undefined) return // signed out, or a server that does not say
      if (get().saving) return // an in-flight choice wins over a stale answer
      show(theme)
      writeStored(theme)
    },

    adoptLocal(p) {
      show(p)
      // This tab's copy of the account must agree, or the next unrelated update
      // of the user (enabling MFA re-sets it from the same copy) would look like
      // the account's value and undo the change.
      const { user, setUser } = useAuthStore.getState()
      if (user && user.theme !== p) setUser({ ...user, theme: p })
    },

    setSystemDark(dark) {
      set({ prefersDark: dark })
      applyTheme(resolve(get().preference, dark))
    },
  }
})

/** The theme actually showing, for code that draws with a colour of its own (admin status colours). */
export function useResolvedTheme(): ResolvedTheme {
  return useThemeStore((s) => resolve(s.preference, s.prefersDark))
}
