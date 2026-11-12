import { useCallback, useEffect, useRef } from 'react'
import {
  prepareRegistration,
  registrationIsFresh,
  REGISTRATION_REFRESH_MS,
  type PreparedRegistration,
} from '@/api/passkeys'

/**
 * Holds passkey registration options fetched ahead of the click that uses them
 * (#632), so that click can call the browser with no network wait in front of
 * it. Safari can refuse a WebAuthn prompt that is not started inside the
 * user's gesture.
 *
 * - Fetches when `enabled` turns on, and again on `refresh()` (after a step-up,
 *   which is what lets the server stage a challenge at all, and after each
 *   attempt, because a challenge is single use).
 * - Refetches before the held set expires. A hidden tab is left alone and
 *   catches up when shown, so an Account page left open does not ask the
 *   server every two minutes for as long as it lives.
 * - A refusal holds nothing and schedules nothing. It is not shown: nobody has
 *   asked for anything yet. The click falls back to fetching for itself.
 *
 * The server keeps one staged challenge per session, so there is only ever one
 * held set and one request in flight, and `take()` hands out the request in
 * flight rather than letting a second start replace it.
 */
export function useRegistrationOptions(enabled: boolean) {
  const held = useRef<PreparedRegistration | null>(null)
  const inflight = useRef<Promise<PreparedRegistration> | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  // True when the timer fired with the tab hidden and the held set was dropped.
  const lapsed = useRef(false)
  const alive = useRef(true)
  // The timer re-enters refresh; a callback cannot name itself, so it goes
  // through this.
  const again = useRef<() => void>(() => {})

  const refresh = useCallback(() => {
    if (inflight.current) return
    clearTimeout(timer.current)
    lapsed.current = false
    held.current = null
    const p = prepareRegistration()
    inflight.current = p
    p.then(
      (r) => {
        if (!alive.current) return
        held.current = r
        timer.current = setTimeout(() => {
          if (document.visibilityState === 'visible') {
            again.current()
          } else {
            held.current = null
            lapsed.current = true
          }
        }, REGISTRATION_REFRESH_MS)
      },
      () => {},
    ).finally(() => {
      if (inflight.current === p) inflight.current = null
    })
  }, [])

  useEffect(() => {
    again.current = refresh
    alive.current = true
    return () => {
      alive.current = false
      clearTimeout(timer.current)
    }
  }, [refresh])

  useEffect(() => {
    if (enabled) refresh()
  }, [enabled, refresh])

  useEffect(() => {
    if (!enabled) return
    const onVisible = () => {
      if (document.visibilityState === 'visible' && lapsed.current) refresh()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [enabled, refresh])

  /**
   * The options for a click to use: the held set (once, it is single use), the
   * request still in flight, or null, in which case the caller fetches for
   * itself. Never waits.
   */
  const take = useCallback((): PreparedRegistration | Promise<PreparedRegistration> | null => {
    const h = held.current
    if (h && registrationIsFresh(h)) {
      held.current = null
      return h
    }
    return inflight.current
  }, [])

  return { take, refresh }
}
