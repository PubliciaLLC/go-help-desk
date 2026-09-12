import { useQuery } from '@tanstack/react-query'
import { getSecurityWarnings } from '@/api/admin'

interface InsecureConfigBannerProps {
  /** Only administrators can see or fix this, so only they are asked. */
  isAdmin: boolean
}

/**
 * Warns administrators that this instance is running on secrets published in
 * the repository.
 *
 * A copied docker/.env.example starts cleanly: its SESSION_SECRET is 47
 * characters, so the 32-character minimum does not catch it. The result is a
 * working instance whose session cookies and API tokens anyone can forge — the
 * worst combination, because nothing appears wrong.
 *
 * Shown to admins only. The backend deliberately keeps this off the public
 * /api/v1/site payload: announcing to anonymous visitors that sessions are
 * signed with a known key is an invitation rather than a warning. The query is
 * gated on the same condition so non-admins never fire a request that 403s.
 */
export function InsecureConfigBanner({ isAdmin }: InsecureConfigBannerProps) {
  const { data } = useQuery({
    queryKey: ['security-warnings'],
    queryFn: getSecurityWarnings,
    enabled: isAdmin,
    staleTime: 5 * 60 * 1000,
    retry: false,
  })

  const insecure = data?.insecure_secrets ?? []
  if (insecure.length === 0) return null

  return (
    <div role="alert" className="border-b border-red-700 bg-red-600 px-4 py-3 text-white">
      <p className="text-sm font-semibold">
        Insecure configuration: {insecure.join(' and ')} {insecure.length === 1 ? 'is' : 'are'} set
        to an example value from the repository.
      </p>
      <p className="mt-1 text-sm text-red-50">
        Anyone can forge sessions or API tokens for this instance. Generate real values with{' '}
        <code className="rounded bg-red-800/60 px-1 py-0.5 font-mono text-xs">
          openssl rand -base64 32
        </code>{' '}
        and restart. Changing {insecure.length === 1 ? 'it' : 'them'} signs everyone out once.
      </p>
    </div>
  )
}
