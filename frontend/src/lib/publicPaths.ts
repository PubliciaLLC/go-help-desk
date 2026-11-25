// The pages that have no sidebar, so no footer to hold the appearance switch
// (#709): sign-in, setup, sign-up, email verification, the password reset pair
// and the guest submit, track and ticket pages. The root route puts the switch
// in the corner of exactly these (components/theme/RootShell.tsx). `/g` is the
// guest ticket view; its address also continues past `/g/`.
export const PUBLIC_PATHS = [
  '/login',
  '/setup',
  '/signup',
  '/verify-email',
  '/forgot-password',
  '/reset-password',
  '/submit',
  '/track',
  '/g',
] as const

export function isPublicPath(pathname: string): boolean {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname
  return PUBLIC_PATHS.some((p) => path === p || (p === '/g' && path.startsWith('/g/')))
}
