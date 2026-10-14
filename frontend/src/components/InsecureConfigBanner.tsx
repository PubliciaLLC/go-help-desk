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

  const staleOff = !!scanning?.stale_off
  const storage = data?.attachment_storage
  const storageBroken = !!storage && !storage.writable

  // An absent field is a server that predates it, which had no way to be off.
  const passkeys = data?.passkeys
  const passkeysOff = !!passkeys && !passkeys.available

  const email = data?.email
  // Email off is a choice for an install that uses nothing which sends mail,
  // so it is only a warning once signup or guest submission is on. Settings
  // shows the plain status either way.
  const emailOff = !!email && !email.enabled && (email.signup_without_email || email.guest_submission_without_email)
  const emailFailing = !!email && email.failing

  if (
    insecure.length === 0 && !scannerDegraded && !staleOff && !storageBroken && !passkeysOff && !emailOff && !emailFailing
  ) {
    return null
  }

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
      {storageBroken && (
        <div role="alert" className="border-b border-red-700 bg-red-600 px-4 py-3 text-white">
          <p className="text-sm font-semibold">Attachment uploads are disabled</p>
          <p className="mt-1 text-sm text-red-50">
            The server cannot write to its attachment directory ({storage.problem}). Uploads are refused until{' '}
            <code className="rounded bg-red-800/60 px-1 py-0.5 font-mono text-xs">ATTACHMENT_DIR</code> exists and the
            server can write to it. Existing attachments can still be downloaded. No restart is needed once it is fixed.
          </p>
        </div>
      )}
      {passkeysOff && (
        <div role="alert" className="border-b border-amber-700 bg-amber-500 px-4 py-3 text-amber-950">
          <p className="text-sm font-semibold">Passkeys are unavailable</p>
          <p className="mt-1 text-sm">
            A passkey is tied to a domain name, so{' '}
            <code className="rounded bg-amber-700/30 px-1 py-0.5 font-mono text-xs">BASE_URL</code> must be a domain
            name (or <code className="font-mono text-xs">localhost</code>) to use them; an IP address or a single-word
            host will not work. Everything else runs normally, but anyone whose only second factor is a passkey cannot
            sign in until an administrator resets their sign-in factors. Reason: {passkeys.reason}
          </p>
        </div>
      )}
      {emailOff && (
        <div role="alert" className="border-b border-amber-700 bg-amber-500 px-4 py-3 text-amber-950">
          <p className="text-sm font-semibold">Email is not set up</p>
          <p className="mt-1 text-sm">
            No email can be sent: {email.missing.join(' and ')} {email.missing.length === 1 ? 'is' : 'are'} not set in{' '}
            <code className="font-mono text-xs">docker/.env</code>. Set{' '}
            {email.missing.length === 1 ? 'it' : 'both'}, then restart the server.
            {email.signup_without_email && ' Self-service signup is on, so nobody can finish signing up.'}
            {email.guest_submission_without_email && ' Guest submission is on, so guests get no link to their ticket.'}
          </p>
        </div>
      )}
      {emailFailing && (
        <div role="alert" className="border-b border-red-700 bg-red-600 px-4 py-3 text-white">
          <p className="text-sm font-semibold">Email is not being delivered</p>
          <p className="mt-1 text-sm text-red-50">
            {email.failing_count} {email.failing_count === 1 ? 'message has' : 'messages have'} failed or are being
            retried. Latest error: {email.last_error}
          </p>
        </div>
      )}
      {staleOff && (
        <div role="alert" className="border-b border-amber-700 bg-amber-500 px-4 py-3 text-amber-950">
          <p className="text-sm font-semibold">Attachments are not being scanned</p>
          <p className="mt-1 text-sm">
            To turn scanning back on, add{' '}
            <code className="rounded bg-amber-700/30 px-1 py-0.5 font-mono text-xs">COMPOSE_PROFILES=antivirus</code>{' '}
            and{' '}
            <code className="rounded bg-amber-700/30 px-1 py-0.5 font-mono text-xs">CLAMAV_ADDR=tcp://clamav:3310</code>{' '}
            to <code className="font-mono text-xs">docker/.env</code>, run{' '}
            <code className="rounded bg-amber-700/30 px-1 py-0.5 font-mono text-xs">docker compose up -d</code>, then
            confirm the scanner address under Settings → Attachments. To keep scanning off, choose
            &quot;Do not scan&quot; there.
          </p>
        </div>
      )}
    </>
  )
}
