import { useQuery } from '@tanstack/react-query'
import { getSecurityWarnings } from '@/api/admin'

interface InsecureConfigBannerProps {
  /** Only administrators can see or fix this, so only they are asked. */
  isAdmin: boolean
}

/**
 * Warns administrators that this instance is running on secrets published in
 * the repository, or that its attachment scanner is not doing what its
 * configuration says it should.
 *
 * The secrets warning: a copied docker/.env.example starts cleanly — its
 * SESSION_SECRET is 47 characters, so the 32-character minimum does not catch
 * it. The result is a working instance whose session cookies and API tokens
 * anyone can forge, the worst combination, because nothing appears wrong.
 *
 * The scanner warning (#176): GET /admin/security-warnings has always
 * carried a live reachability check against the configured scanner, with a
 * plain-English sentence for what it means for uploads right now — but
 * nothing in the UI read it, so an administrator whose ClamAV was down found
 * out only when uploads started failing, or — worse, on a permissive policy —
 * did not find out at all, because files were being stored unscanned and the
 * admin screen said nothing. Shown only when the operator's own policy
 * intends scanning (`policy !== 'off'`) and the live ping failed: an operator
 * who deliberately turned scanning off already sees that choice in Settings
 * and does not need a standing alarm about it.
 *
 * Both shown to admins only. The backend deliberately keeps this off the
 * public /api/v1/site payload: announcing to anonymous visitors that sessions
 * are signed with a known key, or that uploads are going unscanned, is an
 * invitation rather than a warning. The query is gated on the same condition
 * so non-admins never fire a request that 403s.
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
  const scanning = data?.attachment_scanning
  const scannerDegraded = !!scanning && scanning.policy !== 'off' && !scanning.reachable

  if (insecure.length === 0 && !scannerDegraded) return null

  return (
    <>
      {insecure.length > 0 && (
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
      )}
      {scannerDegraded && (
        <div role="alert" className="border-b border-amber-700 bg-amber-500 px-4 py-3 text-amber-950">
          <p className="text-sm font-semibold">Attachment scanner unreachable</p>
          <p className="mt-1 text-sm">{scanning.effect}</p>
        </div>
      )}
    </>
  )
}
