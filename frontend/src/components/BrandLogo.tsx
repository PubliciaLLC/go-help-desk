interface BrandLogoProps {
  showLogo: boolean
  logoURL: string
  siteName: string
  onLogoError: () => void
  imgClassName: string
  fallbackClassName: string
}

// The site's logo, or its name as a fallback — shared by the desktop sidebar,
// the mobile top bar and the drawer, which each show it at a different size.
// Kept to a single image element so the download-only attachment security
// test (see attachments-download-only.test.tsx) has one real allowlist entry
// to check rather than a duplicate to reconcile: mounting it twice (top bar
// + drawer) at once, as the mobile shell does while the drawer is open, is
// not a second remote-content renderer, just the same one instantiated
// twice.
export function BrandLogo({
  showLogo,
  logoURL,
  siteName,
  onLogoError,
  imgClassName,
  fallbackClassName,
}: BrandLogoProps) {
  return showLogo ? (
    <img src={logoURL} alt={siteName} className={imgClassName} onError={onLogoError} />
  ) : (
    <span className={fallbackClassName}>{siteName}</span>
  )
}
