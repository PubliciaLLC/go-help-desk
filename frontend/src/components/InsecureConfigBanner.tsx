import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getSecurityWarnings } from '@/api/admin'
import { ServerNoticeBar, type Notice } from '@/components/ServerNoticeBar'

interface InsecureConfigBannerProps {
  /** Only administrators can see or fix this, so only they are asked. */
  isAdmin: boolean
}

// The release the administrator last dismissed (#170). Per browser, and by
// version, so the next release brings the notice back. Storage can be blocked
// or absent; the notice then simply returns on each load, which is harmless.
const DISMISSED_UPDATE_KEY = 'ghd.update-notice-dismissed'

function readDismissedUpdate(): string | null {
  try {
    return localStorage.getItem(DISMISSED_UPDATE_KEY)
  } catch {
    return null
  }
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
 * The OAuth clients warning (#420): with ticket scope enforcement on, a client
 * whose service user is in no group sees no tickets, and nothing else says so.
 * Its link is a plain anchor, not the router's Link: the banner's tests render
 * it with no router, and the full page load costs nothing here.
 *
 * Presentation: one slim bar, not a stack of banners. It says how many
 * problems there are (or names the one), expands to the full explanation of
 * each, and lets the admin hide a notice for the current session
 * (sessionStorage, keyed by notice id: it returns at the next sign-in while
 * the problem remains, so a failure cannot be silenced for good). Red is for
 * things that are broken now; configuration notices are amber.
 *
 * All shown to admins only. The backend deliberately keeps this off the
 * public /api/v1/site payload: announcing to anonymous visitors that sessions
 * are signed with a known key, or that uploads are going unscanned, is an
 * invitation rather than a warning. The query is gated on the same condition
 * so non-admins never fire a request that 403s.
 */
export function InsecureConfigBanner({ isAdmin }: InsecureConfigBannerProps) {
  const { data, refetch } = useQuery({
    queryKey: ['security-warnings'],
    queryFn: getSecurityWarnings,
    enabled: isAdmin,
    staleTime: 5 * 60 * 1000,
    // A fixed problem must stop showing within seconds of the admin coming
    // back to the tab, not after the stale window. Focus and expand are the
    // only extra requests, which keeps the rate low.
    refetchOnWindowFocus: 'always',
    retry: false,
  })

  // #170: the opt-in update check found a newer release. Information, not a
  // problem, so it is an `info` notice, and its Hide outlasts the session: the
  // release hidden is remembered in localStorage until a newer one appears.
  const [dismissedUpdate, setDismissedUpdate] = useState(readDismissedUpdate)
  const update = data?.update && data.update.latest !== dismissedUpdate ? data.update : undefined

  const insecure = data?.insecure_secrets ?? []
  const scanning = data?.attachment_scanning
  const scannerDegraded = !!scanning && scanning.policy !== 'off' && !scanning.reachable
  // Always a failure: under `required` uploads are refused, under `permissive`
  // files are being stored unscanned right now.

  const staleOff = !!scanning?.stale_off
  const storage = data?.attachment_storage
  const storageBroken = !!storage && !storage.writable

  // #420: OAuth clients that see nothing. Names only; the page they link to
  // shows each client's groups and its service user.
  const blindClients = data?.oauth_clients_without_groups ?? []

  const reopenTarget = data?.reopen_target
  const reopenInactive = !!reopenTarget?.inactive

  // An absent field is a server that predates it, which had no way to be off.
  const passkeys = data?.passkeys
  const passkeysOff = !!passkeys && !passkeys.available

  const email = data?.email
  // Email off is a choice for an install that uses nothing which sends mail,
  // so it is only a warning once signup or guest submission is on. Settings
  // shows the plain status either way.
  const emailOff = !!email && !email.enabled && (email.signup_without_email || email.guest_submission_without_email)
  const emailFailing = !!email && email.failing

  const notices: Notice[] = []
  if (update) {
    const title = `Version ${update.latest} is available (you run ${update.current})`
    notices.push({
      id: `update:${update.latest}`,
      severity: 'info',
      signature: '',
      title,
      hideScope: 'until the next release',
      onHide: () => {
        try {
          localStorage.setItem(DISMISSED_UPDATE_KEY, update.latest)
        } catch {
          // Hidden for this page view only.
        }
        setDismissedUpdate(update.latest)
      },
      body: (
        <p>
          {title}.{' '}
          <a className="font-medium underline" href={update.url} target="_blank" rel="noopener noreferrer">
            Release notes
          </a>
        </p>
      ),
    })
  }
  if (insecure.length > 0) {
    notices.push({
      id: 'insecure-secrets',
      severity: 'failure',
      signature: insecure.join(','),
      title: 'Insecure configuration',
      body: (
        <>
          <p className="font-semibold">
            Insecure configuration: {insecure.join(' and ')} {insecure.length === 1 ? 'is' : 'are'} set
            to an example value from the repository.
          </p>
          <p className="mt-1">
            Anyone can forge sessions or API tokens for this instance. Generate real values with{' '}
            <code className={CODE}>openssl rand -base64 32</code> and restart. Changing{' '}
            {insecure.length === 1 ? 'it' : 'them'} signs everyone out once.
          </p>
        </>
      ),
    })
  }
  if (scannerDegraded) {
    notices.push({
      id: 'scanner-unreachable',
      severity: 'failure',
      signature: scanning.policy,
      title: 'Attachment scanner unreachable',
      body: (
        <>
          <p className="font-semibold">Attachment scanner unreachable</p>
          <p className="mt-1">{scanning.effect}</p>
        </>
      ),
    })
  }
  if (storageBroken) {
    notices.push({
      id: 'attachment-storage',
      severity: 'failure',
      signature: storage.problem,
      title: 'Attachment uploads are disabled',
      body: (
        <>
          <p className="font-semibold">Attachment uploads are disabled</p>
          <p className="mt-1">
            The server cannot write to its attachment directory ({storage.problem}). Uploads are refused until{' '}
            <code className={CODE}>ATTACHMENT_DIR</code> exists and the
            server can write to it. Existing attachments can still be downloaded. No restart is needed once it is fixed.
          </p>
        </>
      ),
    })
  }
  if (reopenInactive) {
    notices.push({
      id: 'reopen-target',
      severity: 'config',
      signature: reopenTarget?.name ?? '',
      title: 'The reopen target status is inactive',
      body: (
        <>
          <p className="font-semibold">The reopen target status is inactive</p>
          <p className="mt-1">
            &quot;{reopenTarget?.name}&quot; has been deactivated, so reopened tickets are going to &quot;New&quot;
            instead. Pick an active status under Settings → Ticket lifecycle → Reopen target status.
          </p>
        </>
      ),
    })
  }
  if (passkeysOff) {
    notices.push({
      id: 'passkeys',
      severity: 'config',
      signature: '',
      title: 'Passkeys are unavailable',
      body: (
        <>
          <p className="font-semibold">Passkeys are unavailable</p>
          <p className="mt-1">
            A passkey is tied to a domain name, so <code className={CODE}>BASE_URL</code> must be a domain
            name (or <code className={CODE}>localhost</code>) to use them; an IP address or a single-word
            host will not work. Everything else runs normally, but anyone whose only second factor is a passkey cannot
            sign in while sign-in MFA is on, until an administrator resets their sign-in factors. If that is the last
            administrator, run{' '}
            <code className={CODE}>docker compose exec app /app/server reset-factors &lt;email&gt;</code>{' '}
            on the server. Reason: {passkeys.reason}
          </p>
        </>
      ),
    })
  }
  if (emailOff) {
    notices.push({
      id: 'email-off',
      severity: 'config',
      signature: `${email.missing.join(',')}|${email.signup_without_email}|${email.guest_submission_without_email}`,
      title: 'Email is not set up',
      body: (
        <>
          <p className="font-semibold">Email is not set up</p>
          <p className="mt-1">
            No email can be sent: {email.missing.join(' and ')} {email.missing.length === 1 ? 'is' : 'are'} not set in{' '}
            <code className={CODE}>docker/.env</code>. Set{' '}
            {email.missing.length === 1 ? 'it' : 'both'}, then restart the server.
            {email.signup_without_email && ' Self-service signup is on, so nobody can finish signing up.'}
            {email.guest_submission_without_email && ' Guest submission is on, so guests get no link to their ticket.'}
          </p>
        </>
      ),
    })
  }
  if (emailFailing) {
    notices.push({
      id: 'email-failing',
      severity: 'failure',
      signature: String(email.failing_count),
      title: 'Email is not being delivered',
      body: (
        <>
          <p className="font-semibold">Email is not being delivered</p>
          <p className="mt-1">
            {email.failing_count} {email.failing_count === 1 ? 'message has' : 'messages have'} failed or are being
            retried. Latest error: {email.last_error}
          </p>
        </>
      ),
    })
  }
  if (blindClients.length > 0) {
    notices.push({
      id: 'oauth-blind-clients',
      severity: 'config',
      signature: blindClients.join(','),
      title: `${blindClients.length === 1 ? 'An OAuth client sees' : 'OAuth clients see'} no tickets`,
      body: (
        <>
          <p className="font-semibold">
            {blindClients.length === 1 ? 'An OAuth client sees' : 'OAuth clients see'} no tickets
          </p>
          <p className="mt-1">
            Ticket scope is on, and {blindClients.join(', ')}{' '}
            {blindClients.length === 1 ? 'acts as a service user' : 'act as service users'} in no group.{' '}
            <a className="font-medium underline" href="/admin/oauth-clients">
              Open OAuth clients
            </a>{' '}
            to add {blindClients.length === 1 ? 'it' : 'each'} to a group, or disable{' '}
            {blindClients.length === 1 ? 'it' : 'them'}. This stays until you do.
          </p>
        </>
      ),
    })
  }
  if (staleOff) {
    notices.push({
      id: 'scanning-off-by-default',
      severity: 'config',
      signature: '',
      title: 'Attachments are not being scanned',
      body: (
        <>
          <p className="font-semibold">Attachments are not being scanned</p>
          <p className="mt-1">
            To turn scanning back on, add <code className={CODE}>COMPOSE_PROFILES=antivirus</code> and{' '}
            <code className={CODE}>CLAMAV_ADDR=tcp://clamav:3310</code> to{' '}
            <code className={CODE}>docker/.env</code>, run <code className={CODE}>docker compose up -d</code>, then
            confirm the scanner address under Settings → Attachments. To keep scanning off, choose
            &quot;Do not scan&quot; there.
          </p>
        </>
      ),
    })
  }

  return <ServerNoticeBar notices={notices} onOpen={() => void refetch()} />
}

const CODE = 'rounded bg-black/10 px-1 py-0.5 font-mono text-xs [overflow-wrap:anywhere]'
