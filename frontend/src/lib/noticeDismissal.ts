export const DISMISS_PREFIX = 'ghd.notice-dismissed.'

/**
 * What a dismissal is keyed on: the notice, its severity and a short summary of
 * what it said. A notice that gets worse (the scanner policy changes, more
 * email fails, another OAuth client goes blind, a second secret is exposed)
 * has a different key, so it comes back instead of staying hidden under the
 * old judgement.
 */
export function noticeKey(n: { id: string; severity: string; signature: string }): string {
  return `${n.id}|${n.severity}|${n.signature}`
}

// Read once per mount; sessionStorage is per tab and gone when it closes,
// which is the lifetime a dismissal should have.
export function readDismissed(): Set<string> {
  const keys = new Set<string>()
  try {
    for (let i = 0; i < sessionStorage.length; i++) {
      const k = sessionStorage.key(i)
      if (k?.startsWith(DISMISS_PREFIX)) keys.add(k.slice(DISMISS_PREFIX.length))
    }
  } catch {
    // Storage blocked: nothing is dismissed.
  }
  return keys
}

export function storeDismissal(key: string) {
  try {
    sessionStorage.setItem(DISMISS_PREFIX + key, '1')
  } catch {
    // Storage blocked: the notice is hidden for this page view only.
  }
}

/**
 * A tab outlives both a sign-out and an expired session (each lands on the
 * login page in the same tab), so without this a hidden notice would stay
 * hidden through the next sign-in, possibly by a different administrator.
 * Called from the logout handler and from the 401 handler.
 */
export function clearDismissedNotices() {
  try {
    for (const k of Array.from(readDismissed())) sessionStorage.removeItem(DISMISS_PREFIX + k)
  } catch {
    // Storage blocked: nothing was stored.
  }
}
