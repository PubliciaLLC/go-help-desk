import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getSiteConfig } from '@/api/admin'
import { siteName, siteTitle } from '@/lib/branding'

/**
 * useSiteBranding reads the public site config and keeps the browser tab title
 * in step with it.
 *
 * /api/v1/site needs no authentication — its handler comment says it exists
 * "used by the SPA shell before login" — so the login screen can call this too.
 * It previously did not, which is why a branded instance showed its name
 * everywhere except the first screen anyone sees (#56).
 *
 * Setting document.title here rather than in index.html is what makes the title
 * configurable at all; index.html can only carry a static default (#57).
 */
export function useSiteBranding() {
  const { data } = useQuery({
    queryKey: ['site-config'],
    queryFn: getSiteConfig,
    staleTime: 5 * 60 * 1000, // refresh at most every 5 min
  })

  const name = siteName(data?.name)

  useEffect(() => {
    document.title = siteTitle(data?.name)
  }, [data?.name])

  return {
    name,
    logoURL: data?.logo_url ?? '',
    version: data?.version ?? '',
  }
}
