// Fallbacks for the public site-branding config.
//
// Two bugs shared a root cause here. The login screen hardcoded
// "Sign in to Go Help Desk" and never read the configured site_name, so a
// branded instance showed its own name everywhere except the one screen every
// user sees first (#56). And index.html shipped Vite's scaffold title, so every
// tab read "frontend" with no way to change it (#57).
//
// Both now go through these two functions, which exist as plain functions
// rather than living inside the hook so the fallback rules can be tested
// without rendering anything.

/** The product's name when an instance has not set one. */
export const DEFAULT_SITE_NAME = 'Go Help Desk'

/**
 * siteName resolves the display name for an instance.
 *
 * A missing setting and a setting of "" or "   " must behave identically: the
 * admin settings form writes an empty string when the field is cleared, and an
 * empty heading reads as a broken page rather than an unbranded one.
 */
export function siteName(configured: string | undefined | null): string {
  const trimmed = configured?.trim()
  return trimmed ? trimmed : DEFAULT_SITE_NAME
}

/** The browser tab title for an instance. */
export function siteTitle(configured: string | undefined | null): string {
  return siteName(configured)
}
