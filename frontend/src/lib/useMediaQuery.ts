import { useSyncExternalStore } from 'react'

// Whether a media query matches now, and again when it changes. Where there is
// no matchMedia (jsdom) it says `fallback`, which is the layout a test sees.
export function useMediaQuery(query: string, fallback: boolean): boolean {
  return useSyncExternalStore(
    notify => {
      if (typeof window === 'undefined' || !window.matchMedia) return () => {}
      const mq = window.matchMedia(query)
      mq.addEventListener('change', notify)
      return () => mq.removeEventListener('change', notify)
    },
    () => (typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(query).matches : fallback),
    () => fallback,
  )
}
