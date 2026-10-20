import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getSecurityWarnings } from '@/api/admin'
import { cn } from '@/lib/utils'
import { DISMISS_PREFIX, readDismissed } from '@/components/noticeDismissal'

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
  const [open, setOpen] = useState(false)
  const [dismissed, setDismissed] = useState<Set<string>>(readDismissed)

  const insecure = data?.insecure_secrets ?? []
  const scanning = data?.attachment_scanning
  const scannerDegraded = !!scanning && scanning.policy !== 'off' && !scanning.reachable
  // Only a required policy refuses uploads when the scanner is down; a
  // permissive one keeps accepting them, so it is a notice, not a failure.
  const scannerBlocks = scannerDegraded && scanning.policy === 'required'

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
  if (insecure.length > 0) {
    notices.push({
      id: 'insecure-secrets',
      severity: 'failure',
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
      severity: scannerBlocks ? 'failure' : 'config',
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

  const visible = notices.filter((n) => !dismissed.has(n.id))
  if (visible.length === 0) return null

  const anyFailure = visible.some((n) => n.severity === 'failure')
  const summary =
    visible.length === 1 ? visible[0].title : `${visible.length} server problems need attention`

  function dismiss(id: string) {
    try {
      sessionStorage.setItem(DISMISS_PREFIX + id, '1')
    } catch {
      // Storage blocked: the notice is hidden for this page view only.
    }
    setDismissed((prev) => new Set(prev).add(id))
  }

  function toggle() {
    // Opening is when the admin is checking whether the fix landed.
    if (!open) void refetch()
    setOpen(!open)
  }

  return (
    <div
      role="alert"
      className={cn(
        'min-w-0 max-w-full border-b text-sm [overflow-wrap:anywhere]',
        anyFailure ? 'border-red-300 bg-red-50 text-red-900' : 'border-amber-300 bg-amber-50 text-amber-950',
      )}
    >
      <div className="flex min-w-0 items-center gap-2 px-3 py-1.5">
        <span aria-hidden="true">⚠</span>
        <span className="min-w-0 flex-1 truncate font-medium">{summary}</span>
        <button
          type="button"
          aria-expanded={open}
          aria-controls="server-notices"
          onClick={toggle}
          className="shrink-0 rounded px-2 py-1 font-medium underline focus-visible:outline focus-visible:outline-2"
        >
          Details
        </button>
      </div>
      <ul id="server-notices" hidden={!open} className="max-h-[60dvh] min-w-0 overflow-y-auto border-t border-black/10">
        {visible.map((n) => (
          <li
            key={n.id}
            className={cn(
              'flex min-w-0 items-start gap-2 border-b border-black/10 px-3 py-2 last:border-b-0',
              n.severity === 'failure' ? 'bg-red-50' : 'bg-amber-50',
            )}
          >
            <div className="min-w-0 flex-1">{n.body}</div>
            <button
              type="button"
              aria-label={`Hide for this session: ${n.title}`}
              onClick={() => dismiss(n.id)}
              className="shrink-0 rounded px-2 py-1 text-xs font-medium underline"
            >
              Hide
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

type Severity = 'failure' | 'config'

interface Notice {
  id: string
  /** failure: something is broken now (red). config: needs setting up (amber). */
  severity: Severity
  title: string
  body: React.ReactNode
}

const CODE = 'rounded bg-black/10 px-1 py-0.5 font-mono text-xs [overflow-wrap:anywhere]'
