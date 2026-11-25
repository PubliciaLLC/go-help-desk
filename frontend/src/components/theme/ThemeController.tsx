import { useEffect } from 'react'
import { isPreference, systemPrefersDark, THEME_KEY } from '@/lib/theme'
import { useAuthStore } from '@/store/auth'
import { useThemeStore } from '@/store/theme'

// Mounted once, beside the router (#709). Connects the theme store to the three
// things that can change the theme without a click on a switch: the account
// (sign-in, /me), another tab (the storage event) and the operating system (the
// media query). The rules themselves are in store/theme.ts.
export function ThemeController() {
  useEffect(() => {
    const { adoptServer, adoptLocal, setSystemDark } = useThemeStore.getState()

    // Whatever the page had before React loaded (public/theme-init.js) is already
    // applied; this only matters when the account is already known.
    setSystemDark(systemPrefersDark())
    adoptServer(useAuthStore.getState().user?.theme)

    // Every way a person gets into the auth store (requireAuth, sign-in, SSO
    // return, the saved answer of a switch) arrives here.
    const unsubscribe = useAuthStore.subscribe((state, prev) => {
      if (state.user !== prev.user) adoptServer(state.user?.theme)
    })

    // A change of the stored value in another tab. That tab has already saved it
    // and written it, so this one only follows. `key` is null when storage is cleared.
    const onStorage = (e: StorageEvent) => {
      if (e.key === THEME_KEY && isPreference(e.newValue)) adoptLocal(e.newValue)
    }
    window.addEventListener('storage', onStorage)

    // The operating system switched (sunset): matters while the choice is device.
    const mq = typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-color-scheme: dark)') : null
    const onSystem = (e: MediaQueryListEvent) => setSystemDark(e.matches)
    mq?.addEventListener('change', onSystem)

    return () => {
      unsubscribe()
      window.removeEventListener('storage', onStorage)
      mq?.removeEventListener('change', onSystem)
    }
  }, [])

  return null
}
