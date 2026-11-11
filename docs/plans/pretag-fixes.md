# 1.3.0 pre-tag fixes: plan for #615–#632 and #173

Status: **plan, not approved.** Nothing here is built until the owner has answered
the decisions in section 3. Base: `origin/v1.3.0-beta` at `df465953` (#614 merged).
Paths are relative to `backend/` unless they start with `frontend/`, `docs/` or `.github/`.
Line numbers are from `bc2f7a14`/`df465953` and will move.

Every issue below was checked against the code. Where the issue text is wrong, the
"Verified" line says so. The open PRs #633, #635, #636, #637 and #638 were read; the
plan does not redo their work and names every file it shares with them.

**Changes since the reading, reported by the coordinator:**
- #614 merged. It touched only `frontend/src/pages/admin/CategoriesPage.tsx` and its tests. The
  CTI editor still hides only fields attached at the same node (`CategoriesPage.tsx:348`), so
  #626(c)'s finding stands.
- #633 now refuses reclassification of Resolved and Closed tickets. (At head `a380b19a` the
  diff I read still allowed it with the SLA frozen. Whoever builds G3 checks the merged code.)
  This does not change the plan for #623: the re-match doors are still priority change, reopen
  and reclassify.
- #637 adds a `webhook_reapproved` audit entry. That does not affect #631, but G8 must still
  extend #637's `filterText` (see #631).

**Not "fixed" by code, said plainly:** #616(a) attestation and #616(c) passkey-required
policy are policy features, not defects, and this plan recommends deciding them rather
than building them before the tag. #629(d) cannot be made real for its audience. #632 and
#619(4) need a person and a real device or tenant. #621 leaves the per-address login limiter
in memory. Each is listed with its honest options.

---

## 1. Per issue

Migrations: the newest is 000059. The search design may take 000060 and 000061. This
plan's migrations are numbered **000062 and up in merge order**. Each up needs a working
down. Recheck the numbers when each PR is opened.

### #615: an administrator cannot remove one passkey of another user
- **Verified:** correct. Passkey routes exist only under `/me` (`server/routes.go:452-455`). The
  admin's only tool is `reset_mfa` → `ClearFactors` (`queries/users.sql:425-460`), which deletes
  every factor and every session. Also: `DELETE /me/passkeys/{id}` ends no other session and
  writes no audit entry.
- **Fix:**
  - Add `GET /admin/users/{id}/passkeys` (same JSON as `/me`; not behind `requirePasskeys`, so
    cleanup works while passkeys are off).
  - Add `DELETE /admin/users/{id}/passkeys/{credId}` behind `DenyMachineCredentials`.
  - New query `RemoveUserPasskey`: one CTE that deletes the credential scoped by `user_id` and
    ends that user's sessions only if a row went.
  - New method `user.Service.AdminRemovePasskey`: detached with `context.WithoutCancel`, then a
    best-effort `writeAuditEntry` (`passkey_removed_by_admin`, `passkey_id` only, never the free-text
    name). This follows the CLAUDE.md decision on `writeAuditEntry`; do not use `ticket.Atomic`.
  - The DELETE refuses the admin's own id with `409`, pointing to the Account page. Without that,
    an admin session that owes no factor could remove its own factor around
    `requireFactorOrFirstEnrolment`, because `adminRouter` checks only `MFAPassed`.
  - Frontend: a passkey list with Remove on `UserDetailPage.tsx`.
  - Last-administrator guard (CLAUDE.md): not affected. The password is still a way in, and
    enrolment stays outside `RequireMFA`.
- **Decisions:** D1, D2, D3.
- **Changes:** no migration. Two new routes and one new audit action (add `passkeyid` to
  `shownKeys`). Not breaking.
- **Size:** M (1 day). **Risk:** scoping by `user_id`, and ending sessions. Test a cross-user
  credential id and the self case.
- **Files:** `server/routes.go` (`/admin/users` block), new `server/handler_admin_passkeys.go`,
  `queries/webauthn.sql` or `users.sql` + `dbgen`, `database/userstore`,
  `domain/user/service.go` (new method), `domain/audit/audit.go` (`shownKeys`, also edited by
  #633 and #637), `frontend/src/pages/admin/UserDetailPage.tsx`, `frontend/src/api/admin.ts`,
  DESIGN.md (~125, ~891, ~1009).

### #616: passkeys. Three separate parts
**(a) Attestation not verified**
- **Verified:** go-webauthn v0.18.2. No `AttestationPreference` is set, so browsers send
  `fmt: none`. The library checks a present statement's signature but not its trust chain. The
  issue's "any authenticator is accepted" is right.
- **Catch:** the library can use the FIDO MDS3 (`Config.MDS`, `metadata/providers/cached`). But
  `ValidateMetadataWithAuthenticatorData` returns nil for `none`, so turning MDS on enforces
  nothing unless we also request `direct` and refuse `none`.
- **What a real build needs:**
  - outbound HTTPS to `mds.fidoalliance.org` at startup;
  - a writable cache, with a refresh we build ourselves;
  - an answer for air-gapped installs;
  - a migration to store the attestation format;
  - a rule for existing credentials, which cannot be checked after the fact.
- **The bigger problem:** synced passkeys (iCloud Keychain, Google Password Manager) generally
  send no attestation. I believe this but have not checked it against current vendor
  behaviour. If so, enforcement means "hardware keys only", which contradicts DESIGN.md's
  deliberate "synced passkeys accepted" (~760-769).
- **Options:**
  - (1) Build an opt-in admin policy, off by default. L (3+ days), needs a migration, risk to
    LAN installs.
  - (2) An AAGUID allow-list without attestation. **Not a control:** the AAGUID is self-reported.
    Do not call this a fix.
  - (3) Document it as a consequence of accepting synced passkeys, and move the build to a later
    milestone.
- **Recommendation:** (3). See D4. This is a policy feature, not a defect.

**(b) Sign count not enforced**
- **Verified:** `domain/webauthn/service.go:217-221` logs `CloneWarning` and nothing branches on
  it. The library sets the flag when the asserted count is ≤ the stored count, unless both are
  zero, so the issue's "goes backwards" is narrower than the truth. Also, the async `Touch`
  writes `sign_count` unconditionally, so two late writes can lower the stored count. That hides
  clones; it does not cause false alarms.
- **Fix:** on `CloneWarning`:
  - refuse the assertion (`401 passkey_suspect`);
  - end the user's sessions;
  - write an audit entry `passkey_clone_suspected` (visible to admins; no migration);
  - change `TouchWebAuthnCredential` to `GREATEST(sign_count, $2)`.
  The key then stays refused until it is removed, by its owner, by #615, or by Reset MFA. A
  passkey-only sole administrator would need `reset-factors`; say so in DESIGN.md and the
  upgrade notes. That case cannot arise until passwordless exists, but write it down.
- **Decisions:** D5.
- **Size:** S–M. **Risk:** an authenticator with a buggy counter locks a real user out. I have
  no data on how common that is.

**(c) Per-role "passkey required" policy**
- **Verified:** `MFARequiredFor` (`domain/admin/service.go:432-444`) is satisfied by either
  factor.
- **What a build means:**
  - a new setting `passkey_required_roles`;
  - a fourth login state, in which TOTP grants `FactorVerified` but not `MFAPassed`, so the
    person can only enrol a key;
  - TOTP enrolment that no longer satisfies the gate for those roles;
  - behaviour while passkeys are off (#494): recommend the setting is ignored, with a banner;
  - an SSO exemption: recommend exempt;
  - an extension of `TestSoleAdministrator_CanSelfRecoverWithNoSecondFactor` to this path. The
    password is still a way in, so the CLAUDE.md condition holds.
- **Size:** L (3–4 days). **Risk:** high regression risk to the login state machine, which has
  had many fixes. Settings keys and `handler_admin_settings.go` collide with #636.
- **Recommendation:** move it out of the tag (D6). If it is built, it is its own PR, last in the
  passkey line.

- **Changes (b only):** no migration and no API shape change. One new error code. Not breaking,
  except that a suspected clone is now refused.
- **Files (b):** `domain/webauthn/service.go`, `server/handler_passkeys.go` (login finish),
  `queries/webauthn.sql` + `dbgen`, DESIGN.md (~788), upgrade notes.

### #617: a TOTP confirm racing a passkey registration
- **Verified:**
  - `SetFirstUserMFA` (`queries/users.sql:403-414`, `WHERE NOT mfa_enabled`) ignores passkeys.
  - `CreateWebAuthnCredential` is a plain INSERT.
  - Both handlers check `requireFactorOrFirstEnrolment` first and only then write
    (`handler_passkeys.go:264/290`, `handler_me.go:300/325`).
  - **#635 (A7) adds only the sequential case:** a passkey committed before the confirm's check.
  - Still open: both requests passing their checks before either writes; and passkey against
    passkey, which the issue does not mention. In both, a password-only session can end up
    adding a factor next to the owner's first one, and possibly keeping the only live session.
    Holding both a TOTP and a passkey is legitimate (DESIGN.md: "TOTP wins"). The wrong part is
    an unverified session adding a factor.
- **Fix (no migration):** write a first factor in one transaction that locks the user row:
  - `SELECT … FROM users WHERE id=$1 FOR UPDATE`;
  - then count the credentials in a new statement (it gets a fresh snapshot under READ
    COMMITTED);
  - write only if the account has no factor.
  - Both doors take the lock:
    - TOTP confirm when not replacing (`ConfirmMFAEnrollmentWith`);
    - passkey `register/finish` always. The "no factor" condition applies only when the session
      is not `FactorVerified`, because an SSO session can be `FactorVerified` with zero local
      factors.
  - The loser gets `403 mfa_required` (frontend already handles it) and writes nothing.
  - A single conditional statement does not work. Under READ COMMITTED, a `NOT EXISTS` re-check
    after waiting for the row lock uses the old snapshot. That is standard Postgres behaviour;
    it was not measured here.
  - Lock order: users, then credentials. This is the same order as `ClearFactors`.
- **Tests:** a deterministic store-level race in which the test transaction holds the user row,
  then releases it. Cover TOTP against passkey and passkey against passkey. Keep #635's test.
- **Decisions:** D7 (minor).
- **Changes:** no migration. No API change beyond a `403` that can now appear in the race. Not
  breaking.
- **Size:** M (1 day). **Risk:** getting the transaction or lock order wrong; flaky tests if
  they rely on timing.
- **Files:**
  - a small store with an `*sql.DB` (as `customfieldstore` has) or new methods in
    `database/webauthnstore`;
  - `domain/user/service.go` (`ConfirmMFAEnrollmentWith`);
  - `server/handler_passkeys.go` (`handlePasskeyRegisterFinish`);
  - `cmd/server/main.go` and `server/server.go` wiring (#635 and #637 touch other hunks);
  - DESIGN.md (~851-855).

### #618: a SAML assertion can be replayed for about 90 seconds
- **Verified:**
  - crewjam/saml v0.5.1. `ServeACS` calls `AssertionHandler.HandleAssertion(assertion)` after full
    validation; I checked this in the module source.
  - We leave it at `NopAssertionHandler`.
  - The tracking cookie is only deleted in the browser.
  - The issue is accurate: a replay needs both the body and the cookie.
- **Fix:**
  - Set `mw.AssertionHandler` in `buildSAMLMiddleware` to a spender that records
    `sha256("saml-assertion\0" + issuer + "\0" + assertionID)` through the **existing**
    `SpendSAMLHandover` insert-on-conflict on `spent_saml_handovers` (migration 000035, 1-day
    purge).
  - A second use fails, and the library's default answer is `403`, the same as any invalid
    assertion.
  - Refuse an empty ID.
  - `HandleAssertion` gets no context, so use `context.WithTimeout(context.Background(), 5s)`.
  - Works across replicas.
- **Decisions:** D8.
- **Changes:** no migration (reuses the table), no API change. Not breaking. If Postgres is down,
  SAML fails closed, as `/complete` already does.
- **Size:** S. **Risk:** an IdP that reuses assertion IDs breaks the spec, and would be refused.
- **Files:** `server/server.go` (`buildSAMLMiddleware`, `SessionStore` interface),
  `database/sessionstore/sessionstore.go`, `server/export_test.go` fake,
  `server/handler_saml_e2e_test.go`, DESIGN.md (~1116).

### #619: SAML. Four separate parts
- **(1) Pasted metadata never refreshed.**
  - **Verified, and understated:** a metadata **URL** is not refreshed while the server runs
    either. `reloadSAML` runs only at startup and on `PUT /admin/saml`. An expired IdP
    certificate fails sign-in (goxmldsig).
  - **Fix:**
    - (a) call `reloadSAML` from the existing hourly ticker (`cmd/server/main.go:529-546`). It
      already keeps the live handler when a build fails. This makes the URL option really
      self-refreshing, and also brings other replicas up to date after a save.
    - (b) add an "IdP signing certificate expires on X" security warning, within 30 days or once
      expired, computed from the live metadata. It goes in `handleGetSecurityWarnings` and
      `InsecureConfigBanner.tsx`.
  - Pasted XML still cannot refresh itself, by definition. Google Workspace only offers a
    download. The warning is the honest signal. Document "use the URL if your IdP has one".
  - S + S.
- **(2) UTF-16 refused.**
  - **Verified:** pasted XML arrives as UTF-8 text (JSON, `FileReader.readAsText`) but keeps
    `encoding="utf-16"` in its declaration, and `xml.Unmarshal` refuses that.
  - **Fix:** in `auth.ParseIdPMetadataXML`, rewrite the declaration's `encoding` to `UTF-8`
    before parsing. Store the XML exactly as pasted. No new dependency.
  - A URL serving real UTF-16 bytes stays out of scope (D9).
  - S.
- **(3) `PATCH /admin/settings` doesn't reload.**
  - **Verified:** this applies only to `saml_metadata_url`, `saml_metadata_xml`, `saml_cert_pem`
    and `saml_key_pem`. `saml_enabled` is read live.
  - **OIDC has the same gap** (`commitOIDCProvider` runs only from startup and `PUT /admin/oidc`).
  - **Fix:**
    - after the `SetRaw` loop, if a SAML configuration key was in the body, call `reloadSAML`;
    - on failure, add `PUT`'s warning text to the response, without echoing the error;
    - do the same for OIDC (D10).
  - S. **Collides with #636** in `handleUpdateSettings`, in another hunk. Land after #636.
- **(4) Entra never verified.**
  - Not fixable in code. Options: (a) someone with an Entra tenant runs a checklist and records
    the results; (b) state "unverified with Entra" in the release notes.
  - The checklist: default claims including `…/displayname`, the NameID format, a user whose
    `mail` is not the UPN, a user with no mailbox, a B2B guest, `email_taken`, and the
    `authnmethodsreferences` MFA claim.
  - Goes with #632 (D19).
- **Changes:** no migration. One additive field in `GET /admin/security-warnings`. Not breaking.
- **Files:** `cmd/server/main.go` (ticker), `server/handler_admin_settings.go` (PATCH; security
  warnings), `server/handler_admin_sso.go` (factor out `touchesSAML`),
  `domain/auth/saml_metadata.go`, `frontend/src/components/InsecureConfigBanner.tsx`,
  `frontend/src/api/admin.ts`, DESIGN.md (~1069), upgrade notes.

### #620: existing users with a non-ASCII email domain refused by the allowed-domains list
- **Verified:**
  - `emailDomain`/`IsEmailDomainAllowed` (`domain/user/service.go:756-783`) compare raw text.
  - The list refuses non-ASCII entries and asks for the `xn--` form.
  - So `a@bücher.de` can never match.
  - In practice only **OIDC** is affected: it checks the address the IdP sends on every sign-in.
    SAML checks only new subjects, and those fail `ValidateEmail` anyway. Signup has its own
    check, after the ASCII rule.
- **Fix:**
  - In `emailDomain`, convert a non-ASCII domain with `idna.Lookup.ToASCII`. `golang.org/x/net`
    is already a direct dependency.
  - On a conversion error, keep the raw text, which still fails closed.
  - ASCII domains are unchanged.
  - The administrator adds `xn--bcher-kva.de`, as the docs already say.
  - Optionally, the save-time `400` names the computed `xn--` form.
  - Rewrite the "known limitation" in DESIGN.md (~1279) and upgrade notes (~524/528).
  - Give admins a query that finds the affected users:
    `SELECT email FROM users WHERE oidc_subject <> '' AND split_part(email,'@',2) ~ '[^\x01-\x7f]';`
- **Decisions:** D11.
- **Changes:** no migration, no API shape change. Not breaking; it only widens matching.
- **Size:** S. **Risk:** low (UTS-46 edge cases; test them).
- **Files:** `domain/user/service.go` (`emailDomain` only), its tests, optionally
  `server/handler_admin_settings.go` (the message, ~677; #636 edits another hunk), DESIGN.md,
  upgrade notes.

### #621: rate limits in memory, per process
- **Verified:**
  - The citations are right.
  - **Corrections:** the guest upload rate is keyed per **ticket**, not per link. The issue
    leaves out the companion `guestRefusalLimiter` (20 refusals an hour per ticket), which has to
    move with the upload limiter.
  - Other in-memory limiters:
    - `loginLimiter` (per account and per address; Tarpit);
    - `guestResendLimiter` (1 per 5 minutes per ticket);
    - `templateTestLimiter`;
    - `reputation.Budget`, which is a courtesy cap by design.
  - **Deployment model:** DESIGN.md:37 says Docker Compose, with Kubernetes as "v4". So
    multi-replica is not a supported v1 target, yet the code already assumes replicas in several
    places. Losing counts on restart applies to every deployment.
- **Honest options:**
  - (a) Durable counters for the named limits. Recommended: the hybrid below.
  - (b) Durable counters for every limiter, including the login one. Its keys are chosen by an
    attacker (addresses, emails), so the table grows without bound. Its durable fix is already
    planned separately (`ratelimit.go:32`, on the user row).
  - (c) Documentation only. It already exists. **That would not be a fix.**
- **Fix (hybrid):**
  - **Forgot password, no migration:** derive it from `password_reset_tokens.created_at` (one row
    per account). The first attempt becomes `INSERT … ON CONFLICT (user_id) DO UPDATE … WHERE
    created_at <= now - 5 min` as `:execrows`; zero rows means "send nothing".
    - Retries keep the unconditional upsert.
    - Side effect: a redeemed link deletes the row, so a new request inside 5 minutes is
      allowed. Document it.
    - Remove `passwordResetLimiter`.
  - **One table, `rate_limit_counters(key text PK, window_start timestamptz, count int)`**
    (migration), for `reporter_email`, guest uploads, guest refusals and guest resend:
    - take: a fixed-window upsert that returns the count;
    - refund: an `UPDATE … WHERE key AND window_start AND count>0`;
    - `Retry-After` comes from that key's own window;
    - a daily sweep on the existing ticker;
    - the time comes from the Go clock, so the test seams survive;
    - fail closed on a database error.
    - Every key is a real user or ticket, so an attacker cannot grow the table.
  - `reporter_email` and guest uploads **cannot** be derived from existing rows:
    - on-behalf has no `filed_by` column, and refusals spend units, which is the point of #444;
    - post-scan refusals leave no row, so counting would bring back "twenty bad PNGs = twenty
      scans".
  - Leaves `loginLimiter` and `templateTestLimiter` in memory, documented.
- **Decisions:** D12, D13, D14.
- **Changes:** one migration (the counter table). No route change. `429` and `Retry-After`
  semantics change from one window shared by every key to a window per key, which is stricter.
  Update DESIGN.md (~1395, ~1723, forgot-password) and upgrade notes (~117, ~213).
- **Size:** M (2 days). **Risk:** `handler_guest_attachment_limits_test.go` encodes the
  shared-window drift; expect test rewrites (changed, never weakened).
- **Files:**
  - `server/server.go` (limiter fields and wiring);
  - `server/handler_guest.go` (~375-407, ~546);
  - `server/handler_tickets.go` (~623, on-behalf);
  - `server/handler_password_reset.go`;
  - `domain/passwordreset/*` and its store;
  - `queries/password_reset.sql` and a new `queries/rate_limit.sql`, plus `dbgen`;
  - new `database/ratelimitstore`, a migration, `cmd/server/main.go` (sweep).
  - Collisions: `server.go` (#635, #637), `main.go` (#633, #636, #637), `handler_guest.go` and
    `handler_tickets.go` (#633; G6 also edits them).

### #622: OAuth client secrets cannot be rotated
- **Verified:**
  - Correct: the routes are list, create and delete (`routes.go:343-351`).
  - Only `SHA-256` of a 256-bit secret is stored.
  - Access tokens are stateless HS256 JWTs valid for 1 hour (`domain/auth/oauth.go:42`), with
    nothing tied to the secret.
  - `BearerAuth` already loads the client on every request.
- **Fix:**
  - Add `POST /admin/oauth-clients/{id}/rotate-secret` behind `RequireResource(credentials)` and
    `DenyMachineCredentials` (minting a secret is creating a credential). It answers `200
    {id, client_id, client_secret}`; the `client_id` and service user stay the same.
  - Store: `RotateOAuthClientSecretAudited`, on #637's `inTx` + `GetOAuthClientForUpdate` +
    `writeEntry` pattern. Audit action `oauth_client_secret_rotated`. Use no key with "secret" in
    its name, because `isSensitive` would redact it.
  - **Revoke issued tokens without a migration:** add an `sv` claim (the first 16 hex characters
    of `HashedSecret`) at issue, and have `BearerAuth` refuse a mismatch. Tokens with no `sv`
    (issued before the upgrade) are accepted, so nothing breaks; they expire within an hour.
  - Frontend: "Rotate secret" with a confirmation on `OAuthClientsPage.tsx`, reusing
    `SecretBanner`.
- **Decisions:** D15, D16.
- **Changes:** no migration (immediate replacement). One new route (additive) and one new JWT
  claim. Not breaking.
- **Size:** S–M (1 day). **Risk:** the `sv` check rejecting legacy tokens or tokens from a new
  client. Test both.
- **Files:**
  - `server/routes.go`, `server/handler_admin_credentials.go`, `server/credentials_audit.go`
    (#637);
  - `database/authstore/authstore.go` (#637), `queries/auth.sql` + `dbgen`;
  - `server/server.go` (`AuthStoreIface`);
  - `domain/auth/oauth.go`, `middleware/auth.go` (#635 edits `APIKeyAuth` there);
  - `server/handler_auth.go` (token issue; #635 edits the compare);
  - frontend `OAuthClientsPage.tsx`, `api/admin.ts`, `CredentialAuditContext.tsx` (#637).
  - **Land after #635 and #637.**

### #623: SLA first-response and resolution recording race a re-match
- **Verified:**
  - **Five** call sites, not four: `ticket/service.go:1001` (`recordResolvedAfterStatus`), 1944,
    2104, 2253 and 2398.
  - The writes (`queries/sla.sql:75`, `:133`) are keyed on `ticket_id AND superseded_at IS NULL`.
  - **The issue describes only half the race.**
    - Ordering 1 (the issue's): the write waits on the superseded record's lock and then updates
      nothing.
    - Ordering 2: the re-match commits between the read and the write, and the write lands on
      the **new** record with elapsed time computed from the **old** clock and policy. After a
      reopen restart, that records a response from before the restart as met on the new clock.
      DESIGN.md calls the restart case "correct"; in this ordering it is not.
  - "Errors are discarded" is true only in the domain. `cmd/server/sla_wiring.go` (`loggingSLA`)
    logs every failure at the boundary, which matches CLAUDE.md. Keep it.
- **Fix:**
  - Key the writes on the record id: `WHERE id=$1 AND superseded_at IS NULL`, as `:execrows`.
  - `Record*` reads the current record. It skips if the record started after the event (this
    closes ordering 2), computes, writes by id, and on zero rows re-reads and retries up to 3
    times. If every attempt fails, it returns a wrapped error so `loggingSLA` logs it.
  - Moving `Record*` into the reply's transaction is rejected: it would pull SLA logic into
    `ticket.Store` and let an SLA failure abort a reply, which DESIGN.md forbids.
- **Decisions:** D17.
- **Changes:** no migration, no API change. Only internal store signatures change.
- **Size:** S. **Risk:** the concurrency test; hold locks explicitly, never sleep.
- **Files:** `queries/sla.sql` + `dbgen`, `domain/sla/{service.go,store.go,service_test.go}`,
  `database/slastore/slastore.go`, DB tests (`internal/database`), the existing tests that call
  the store methods, DESIGN.md (~4870; #633 edits the same section). **No change to
  `ticket/service.go`.**

### #624: the ticket list cannot filter on source_channel
- **Verified:**
  - Correct: the column is never in a `WHERE`.
  - `ListTicketsFiltered` (`queries/tickets.sql:373`, used by MCP) is the only composable query.
  - Requesters never receive the field, so a requester filter on it would leak it and must be
    refused.
- **Options:**
  - (A) Fold it into the advanced-search proposal, which already specifies exactly this filter.
    That proposal is not approved (about 9 coder-days).
  - (B) A minimal version now, built on the same pieces advanced search would add:
    - `ticket.Filter.SourceChannels`;
    - the proposal's SQL line in `ListTicketsFiltered`;
    - an MCP `list_tickets` argument;
    - REST `?source_channel=` (repeatable, enum-checked, at most 5), which takes one new
      branch: everything visible plus `q`, through `ListFiltered`;
    - `403` for requesters; `400` when combined with `reporter_id`, `assignee_group_id` or a
      scope other than `visible`;
    - a small staff "Channel" select on the list.
    Only that REST branch is thrown away when the proposal's handler rewrite lands.
- **Decisions:** D18.
- **Changes:** no migration. An additive REST parameter and MCP argument. Not breaking.
- **Size:** S–M (1 day). **Risk:** the new branch's interaction with `scope`; pin the 400 and 403
  cases in tests.
- **Files:** `domain/ticket/filter.go`, `queries/tickets.sql` (`ListTicketsFiltered` only) +
  `dbgen`, `database/ticketstore`, `server/handler_tickets.go` (`handleListTickets` only),
  `internal/mcp/server.go` (`buildListFilter`, schema; #633 and #636 touch other parts),
  `docs/api/openapi.yaml`, `frontend/src/pages/TicketListPage.tsx`, `frontend/src/api/tickets.ts`.
  These are the same files as advanced-search PRs A, B, C, E and F.

### #625: closed-ticket follow-up. Eight limits
1. **Guest follow-up gets the "We have received" mail.**
   - **Fix:** add a typed `FollowUp` flag on `notification.Event` (round-tripped through the
     codec) and a new template `ticket_follow_up_created.tmpl`. Templates are a Go registry plus
     embedded files, so no seed migration is needed.
   - Wording: D20. S.
2. **A lost double-submit burns a tracking number.**
   - **Verified:** `NextSeq` runs before `InTx` (`service.go:665` vs 700-710).
   - **Fix:** take the number inside the transaction, after the locked follow-up check, and reset
     it on every retry.
   - Gaps from a rollback after `nextval` remain; that is inherent to sequences. Document it.
   - S.
3. **Any `parent_child` link counts as the follow-up.**
   - **Fix:** migration `ticket_links.follow_up boolean NOT NULL DEFAULT false`, backfilled `true`
     for every existing `parent_child` row. Existing data behaves as today; only new hand-made
     links stop counting.
   - `create` sets the flag, and `refuseSecondFollowUp` reads it.
   - Reverse the pinned test `a_hand-made_parent_link_counts`. That is a decision (D21), not a
     weakened test.
   - Not a new link type, which would change an enum clients see. S–M.
4. **Deleted reporter → 400.**
   - **Verified, and DESIGN.md is wrong about the cause:** deletion is soft, and the refusal
     comes from `UserExists` ("reporter_user_id is not a user…"), not from the reporter-or-guest
     rule.
   - Filing with no reporter is impossible without dropping a CHECK constraint.
   - **Fix:** `409 reporter_deleted`, saying "restore the account or open a new ticket". Add it to
     openapi and the MCP error mapping (#636 edits that mapping).
   - D22. S.
5. **Expired guest tokens never swept.**
   - **Verified:** no delete query exists. Migration 000022 even built the index "for the expiry
     sweep", which was never written.
   - **Fix:** `DeleteExpiredGuestAccessTokens` in the existing hourly sweep. S.
6. **Follow-up button stays after a reload.**
   - **Fix (needs item 3):**
     - `follow_up: {id, tracking_number} | null` on the single-ticket view of a Closed ticket
       (the newest link marked `follow_up`), added to the requester allow-list;
     - `has_follow_up` on the guest ticket response;
     - the frontend hides the button and shows the link (guests get text only).
   - D23. M.
7. **Staff composer hidden on closed tickets.**
   - **Verified:** `TicketDetailPage.tsx:620` hides it for every role, while DESIGN.md:199 and the
     service allow staff replies.
   - **Fix:** show it to staff and admins on Closed tickets, with "Replying does not reopen this
     ticket". No decision needed. S.
8. **Custom-field values not copied.**
   - **Fix:** a `ticket.Service` hook (wired like `SetUserEmailLookup`) that calls
     `customfield.Service.CopyValues` after commit.
     - It copies the values whose field resolves on the new ticket's classification.
     - For a requester's follow-up, it copies only `visible_on_new` fields.
     - Failures are logged at the boundary, because the ticket is already committed.
     - Not atomic. Document that, as for ordinary creation values.
   - D24. M. **Built in G6** (which owns `customfield/service.go`), after G5 merges.
- **Changes:** one migration (item 3). New response fields `follow_up` and `has_follow_up`
  (additive), a new error code `reporter_deleted`, and a new email template. Not breaking,
  except the behaviour change in item 3, which is the point of the fix.
- **Size:** L overall (about 3 days for items 1–7, plus item 8 in G6).
- **Files:**
  - `domain/ticket/service.go` (`create`, `CreateFollowUp`, `refuseSecondFollowUp`; #633 does not
    touch these);
  - `domain/notification/{notification.go,codec.go}` (also G4);
  - `server/notify/{email.go,templates.go,templates/*.tmpl}`;
  - migration, `queries/tickets.sql` (link queries) and `queries/guest_tokens.sql` + `dbgen`,
    `database/ticketstore`;
  - `server/ticket_view.go` (also G7), `domain/ticket/requester_view.go`;
  - `server/respond.go`, `internal/mcp/server.go` (error mapping);
  - `cmd/server/main.go` (sweep);
  - `frontend/src/pages/TicketDetailPage.tsx`, `GuestTicketViewPage.tsx`;
  - DESIGN.md (~300-410; #633 edits ~295).

### #626: custom fields. The API skips four rules
- **Verified:** every claim holds, plus one correction:
  - **(c) the UI *can* make an overlap.** The CTI editor hides only fields attached at the same
    node (`CategoriesPage.tsx:348`, still so after #614). Only the Custom Fields page guards
    against it.
- **(a) Required.**
  - `customfield.Service.CheckRequired` uses the form's rule (the most specific attachment is
    `VisibleOnNew && RequiredOnNew` and the field is active).
  - Call it in `handleCreateTicket` **before** `Create`, so no tracking number is spent, and
    after the requester's item has been cleared.
  - At creation only.
  - A known race: an admin edits the field between the check and the create. Accept and document
    it.
  - **Breaking for API clients** (D25).
- **(b) Options.**
  - Refuse blank (after trim) and exact-duplicate options across `options` and
    `retired_options`.
  - Check only when a list actually changes, so a rename of an already-bad field still saves.
- **(c) Overlap.**
  - In `customfieldstore.CreateAssignment`, lock the definition row, then look for the same field
    at an ancestor or descendant. Answer `409` naming where it is attached.
  - Existing overlaps stay ("most specific wins"); no migration.
  - Rewrite `TestAttachOverlapping_IsNotRefused` to seed the overlap through the store (D26).
  - #638 makes the `409` visible in `CategoriesPage.tsx`; land it first.
- **(d) `retired_options` on ticket routes.**
  - Select the column in `ListAssignmentsForScope` and `ListCustomFieldValuesForTicket`.
  - Add `RetiredOptions` (omitempty) to `TicketFieldValue`, plus openapi and `types.ts`.
- **Decisions:** D25, D26, D27.
- **Changes:** no migration, no new route. New `400`s (a, b), a new `409` (c), a new response key
  (d). (a) is breaking.
- **Size:** M (1.5 days). **Risk:** (a)'s breaking change and its churn in docs and tests.
- **Files:** `domain/customfield/{service.go,customfield.go,service_test.go}`,
  `database/customfieldstore/customfieldstore.go`, `queries/custom_fields.sql` + `dbgen`,
  `server/handler_tickets.go` (create, ~400-502; #633 renames `routing` nearby),
  `server/handler_custom_fields_resolve_test.go`, `docs/api/openapi.yaml`, DESIGN.md, upgrade
  notes, `frontend/src/api/types.ts`.

### #627: guests and MCP cannot send custom fields
- **Verified:**
  - Correct.
  - A guest has **no route** to discover fields: `GET /tickets/fields` is behind `RequireRole`.
  - MCP has no field-discovery tool, so `custom_fields` alone would give an agent ids it cannot
    find.
  - `get_ticket` returns no values.
  - DESIGN.md:4632 states the opposite of this work as a deliberate decision. The owner's
    2026-11-10 decision overrides it; rewrite it and upgrade notes ~411.
- **Disclosure risk:**
  - There is no "visible to guests" flag. `visible_on_new` also drives the signed-in form that
    staff use.
  - Reusing it would, on upgrade, publish field names and options (possibly internal ones) to
    anonymous visitors, and let them write values.
- **Fix:**
  - **Guests:**
    - an opt-in (D28);
    - `GET /guest/fields?category_id=` behind `guest_submission_enabled` and the opt-in,
      returning a narrow shape (id, name, type, options, required);
    - `custom_fields` on `POST /guest/tickets`, validated against exactly the offered set, with
      the required check before `Create` (shared with #626a);
    - a `400` for a bad value rather than warnings (D29);
    - restore the section in `GuestTicketPage.tsx`;
    - a rate limit on the new GET.
  - **MCP:**
    - `create_ticket.custom_fields` (string values, as REST);
    - a new tool `list_ticket_fields(category_id, type_id?, item_id?)`;
    - optionally values on `get_ticket` (D30);
    - wire the service with a setter, not another `mcp.New` argument, because #633 already
      changed `mcp.New`.
- **Decisions:** D28, D29, D30.
- **Changes:**
  - a migration only if the per-attachment flag is chosen;
  - new route `GET /guest/fields`, new body field `custom_fields`, new MCP argument and tool.
  - Not breaking, unless #626a's required rule applies to a door's offered fields. Ship them
    together.
- **Size:** L (3 days) with the flag, M with a global setting. **Risk:** a new unauthenticated
  read and write; it needs a security review.
- **Files:**
  - `server/handler_guest.go` (#633 edits the same function), `server/routes.go` (guest block);
  - `internal/mcp/server.go` (`create_ticket`, new tool; #633 and #636), `cmd/server/main.go`;
  - `domain/customfield/service.go`, `domain/ticket/requester_view.go` (only if warnings go to
    guests);
  - `frontend/src/pages/GuestTicketPage.tsx`, `frontend/src/api/tickets.ts`.
  - With the flag, also: migration, `queries/custom_fields.sql`, `customfieldstore`,
    `CategoriesPage.tsx` (#614 and #638), `customfield/audit.go`.

### #628: quarantined attachments. No list marker, no MCP verdict
- **Verified:**
  - Correct. Quarantined means `attachments.virus_name IS NOT NULL`, indexed by `ticket_id`.
  - Every attachment is visible to anyone who can see the ticket; REST returns `virus_name` to
    requesters.
  - I could not check the #168 comment the issue cites; it is on GitHub, which I did not read.
- **Fix:**
  - **List marker:**
    - do not edit the roughly 12 list queries. Every REST list goes out through `ticketViews`
      (`server/ticket_view.go:113`), which is staff-only and already batches SLA per page.
    - Add one query, `SELECT DISTINCT ticket_id FROM attachments WHERE ticket_id = ANY($1) AND
      virus_name IS NOT NULL`, and `has_quarantined_attachment` (omitempty) for staff.
    - Add a quiet badge next to `ReopenedBadge` in `TicketListPage.tsx`.
    - This is compatible with the advanced-search proposal, which keeps `ticketViews`.
  - **MCP:** add `list_attachments(ticket_id)` with `mcpRead` scope, for any user who can see the
    ticket.
    - The ticket is looked up the same way as in `get_ticket`.
    - It returns metadata plus `virus_name`, `mismatch` and a derived `quarantined`.
    - No content (CLAUDE.md: download-only) and no reputation lookup (D32).
    - Fix the "MCP has no attachment tool" wording (DESIGN.md:2677, `mcp/server.go:395`).
- **Decisions:** D31, D32.
- **Changes:** no migration. An additive response key and a new MCP tool. Not breaking.
- **Size:** M (1–1.5 days). **Risk:** small; tests that list every `ticketView` key will need the
  new key.
- **Files:** `queries/tickets.sql` (one new query) + `dbgen`, `database/ticketstore`,
  `domain/ticket` (Store interface, service, fakes), `server/ticket_view.go` (also G5),
  `internal/mcp/server.go`, `frontend/src/api/types.ts`, `frontend/src/pages/TicketListPage.tsx`
  (also G11), DESIGN.md.

### #629: audit view. Four separate items
- **(a) `q` does not reach the diff.**
  - **Verified:** `audit.Filter`'s comment gives the reason. Matching the payload would be an
    **oracle on redacted values**, such as the ticket subject.
  - **Fix:**
    - search only what `Redact` would show: pass `audit.SearchableKeys()` (the shown keys minus
      the sensitive ones) as a `text[]`;
    - match scalar values of those keys in `before`/`after` with `ILIKE`;
    - escape `%` and `_` (today's handling was not checked);
    - a parity test proves a hidden value is never a hit.
  - The cost is per-row jsonb expansion on a scan that is already full; measure it on a large
    table, as #331 did.
  - M. Land after #637 (its new keys become searchable).
- **(b) Actor by id.**
  - **Correction:** the UI already picks by name from a staff dropdown. The real gap is actors
    who are not in that list (OAuth service users, disabled or deleted staff, requesters) and API
    callers.
  - **Fix:** widen `q` to match actor display name and email, and exclude requesters while
    `audit_mask_requester_names` is on, so the search is not a way round the mask.
  - S.
- **(c) Purge is one DELETE.**
  - **Fix:** batches of 5,000 with `SKIP LOCKED` inside `auditstore.DeleteOlderThan` (same
    interface). Check `ctx` between batches and return the running total. Precedent:
    `autoCloseBatch`.
  - S.
- **(d) The mask is display-only.**
  - **Cannot be made real for this audience.** Only administrators see the admin log. They can
    open the ticket named by `entity_id`, and `GET /admin/users/{id}`.
  - Hiding `actor_id` would break the documented "stays attributable" promise and the
    `actor_id` filter. It would be an API break that stops nobody.
  - DESIGN.md:495, the code comment and the Settings help already say it is display-only.
  - **Recommendation:** no code change beyond (b)'s exclusion. D35.
- **Decisions:** D33, D34, D35.
- **Changes:** no migration, no new parameter (`q` widens). Not breaking.
- **Files:** `queries/audit.sql` + `dbgen`, `database/auditstore`, `domain/audit/audit.go`
  (#633 and #637), `server/handler_admin_audit.go`, `frontend/src/pages/admin/AdminAuditPage.tsx`
  (#637), DESIGN.md, `docs/api/openapi.yaml`.

### #631: the webhook ticket filter has no type or item condition
- **Verified:**
  - Correct.
  - The filter is JSONB (`webhook_configs.ticket_filter`, migration 000038), so **no migration**.
  - `TicketState` already carries `TypeID` and `ItemID`.
- **Fix:**
  - Add `TypeIDs` and `ItemIDs` to `notification.WebhookFilter`, matched with AND as now.
  - Four places that each silently widen or break a hook if they are missed:
    1. `IsEmpty()` must count them (`webhook_filter.go:23`); otherwise a types-only filter admits
       everything.
    2. The redelivery state (`server/webhook_source.go:37`) must set `TypeID` and `ItemID`.
    3. #637's `filterText` must include them; otherwise a types-only edit writes no audit entry.
    4. `checkFilter` (strict decoding) must accept them, validate them through categories, and
       cap their length.
  - Frontend: Type and Item groups under the ticked categories and types.
- **Decisions:** D36, D37.
- **Changes:** no migration. Additive fields. A downgrade to an older binary ignores the new keys
  and widens the hook; say so in the upgrade notes. Not breaking.
- **Size:** M (1.5 days). **Risk:** missing one of the four places.
- **Files:** `domain/notification/webhook_filter.go` + test, `server/webhook_source.go`,
  `server/handler_admin_credentials.go` (`checkFilter`; #637), `server/credentials_audit.go`
  (#637), `frontend/src/api/types.ts`, `frontend/src/pages/admin/WebhooksPage.tsx` (also G4,
  which changes `EVENTS`), DESIGN.md (~3526), openapi, upgrade notes. **Land after #637.**

### #630: writes that announce nothing
- **Verified:**
  - `RemoveLink` (`service.go:2582`) is a bare delete: no actor, no transaction, no event.
  - Attachment creates ignore the change collector.
  - **"Removed attachment" is wrong:** `DeleteAttachment` has no route and no production caller.
  - `UnassignForUser` and `DeleteGroup` take `_ *notification.Changes`.
  - `reply_id` is already in the event payload; only `rawKeysFor` (`notify/webhook_body.go:33`)
    keeps it off the wire.
- **Fix:**
  1. `reply_id` on raw `ticket.replied` and `ticket.note_added` bodies. Additive. XS.
  2. `RemoveLink`: take an actor, run in `InTx`, lock both tickets in `AddLink`'s order (to avoid
     the #210 deadlock), and raise a new `ticket.unlinked` with `target_id` and `link_type`. No
     mail. S.
  3. Upload: a new `ticket.attachment_added` with `attachment_id`, read inside the transaction.
     No mail (D39). An upload plus a reply means two deliveries. S.
  4. Follow and unfollow: **do not announce.** Document it (D40). They are personal preferences,
     and announcing them would leak what `staff_can_view_follow_audit` hides by default.
  5. `UnassignForUser` and `DeleteGroup`:
     - raise `ticket.assigned` (cleared) per ticket, webhook only, actor = the deleting admin;
     - **staff mail must be suppressed** with a typed `QueueReturn` flag, or a group delete mails
       "assigned to you" in a burst (`notify/staff.go:212-228`);
     - test that a group delete mails nobody;
     - one event per ticket is the #506 contract. M.
- **Event names live in four places:** `notification.WebhookEvents`, `KindOrder`, the openapi
  `WebhookEvent` enum, and the frontend `EVENTS`. Also update `notify/format.go`.
- **Decisions:** D38, D39, D40, D41.
- **Changes:** no migration. Two new webhook event names and new raw keys, all additive. `*` and
  all-events hooks start receiving the new names; put that in the upgrade notes. Not breaking.
- **Size:** M (2 days). **Risk:** false staff mail if the suppression is missed.
- **Files:**
  - `domain/ticket/service.go` (`RemoveLink`, `UnassignForUser`, `DeleteGroup`,
    `CreateAttachment`, `CreateGuestAttachment`; no overlap with #633's functions);
  - `domain/notification/{notification.go,channels.go,changes.go,codec.go}` (also G5);
  - `server/notify/{webhook_body.go,format.go,staff.go}`;
  - `server/handler_tickets.go` (the `RemoveLink` handler);
  - `docs/api/openapi.yaml`, DESIGN.md (~3753-4325; #633 edits section 8);
  - `frontend/src/pages/admin/WebhooksPage.tsx` (also G8).

### #632: never verified in a real environment
- **Verified:**
  - **(1) Passkeys:** only jsdom tests, and Playwright mocks every backend call. The Safari risk is
    real in the code: after step-up, `useStepUp` re-runs `navigator.credentials.create` with no
    new click (`AccountPage.tsx:188-215, 362`).
  - **(2) arm64:** **partly stale.** CI already cross-builds `linux/amd64,linux/arm64` with buildx
    and QEMU (`.github/workflows/ci.yml:302`), and the release publishes both. Never done: a native
    arm64 host running `docker compose … build` and then **running** the image.
  - **(3) SAML:** the fake-IdP end-to-end tests exist (`handler_saml_e2e_test.go:84`). It was
    never run against a real IdP.
- **Honest options for each:**
  - **(1)**
    - code (S): after step-up, show a "Verified, add the passkey" button instead of retrying
      automatically. This removes the known Safari hazard without guessing Safari's gesture rules.
    - plus a manual checklist (1–2 hours): Safari on macOS, Safari on iOS and one hardware key;
      register, sign in, step-up then add, remove.
    - Optional: a Chromium-only virtual-authenticator Playwright test.
    - Safari WebAuthn cannot be automated here. My understanding is that Playwright's virtual
      authenticator is Chromium/CDP only; not verified.
  - **(2)**
    - a CI job on a GitHub arm64 runner (`ubuntu-24.04-arm`) that runs the Compose build and
      reuses the image smoke test. S–M. Whether that runner is available on this plan was not
      checked.
    - plus one manual run on Apple Silicon.
    - The clamav profile is amd64-only; leave it out.
  - **(3)** a manual checklist against Okta Developer (free), Entra (the same run as #619(4)) and
    Google Workspace if available. Anything not run is listed as "not verified" in the release
    notes. Keycloak in CI is possible later (M–L).
- **Decisions:** D19.
- **Changes:** no migration, no API change. One extra click when adding a passkey after step-up.
- **Size:** S + S–M + about 1 day of manual work. **Risk:** the manual runs find real bugs late.
  Schedule them on the release-candidate build, not after the tag.
- **Files:** `frontend/src/pages/AccountPage.tsx` + tests, `.github/workflows/ci.yml`, a
  checklist in `docs/` (or the release notes).

### #173: the reputation summary does not say which provider it came from
- **Verified:** correct. `inline` is on the wire (`frontend/src/api/types.ts:456`, set at
  `domain/ticket/ticket.go:548`), and `ProviderLine` (`AttachmentList.tsx:509-557`) never reads it.
  The existing test fixtures already send it, which is why the earlier closure passed without a
  marker.
- **Fix:**
  - When `inline` is set, add a quiet grey marker on that line ("· shown in the summary"), with
    `aria-current="true"`.
  - Tests: the marker appears only on the line with `inline`; it appears nowhere when nothing
    answered; it is absent in the single-provider layout.
  - Rewrite DESIGN.md:2402-2408.
- **Decisions:** D42 (the wording).
- **Changes:** none. **Size:** S (2 hours), almost no risk.
- **Files:** `frontend/src/components/ticket/AttachmentList.tsx`,
  `frontend/src/pages/attachments-reputation-providers.test.tsx`, DESIGN.md. No open PR touches
  them.

---

## 2. PRs, file ownership and merge order

**Wave 0 (already open):** #633, #635, #636, #637 and #638 merge first. Every PR below
branches from `v1.3.0-beta` **after** the open PRs it depends on have merged, so nobody rebases
onto moving work.

| PR | Issues | Owns (beyond docs) | Waits for | Size |
|---|---|---|---|---|
| **G1 passkeys** | #615, #617, #616(b) (+ #616(a)/(c) only if D4/D6 say build, as a separate later PR) | `routes.go` `/admin/users` block, `handler_passkeys.go`, new `handler_admin_passkeys.go`, `queries/webauthn.sql`, `queries/users.sql` (first-factor/remove queries), `domain/webauthn/service.go`, `domain/user/service.go` (`ConfirmMFAEnrollmentWith`, new `AdminRemovePasskey`), new first-factor store, `UserDetailPage.tsx` | #635, #637 | L (≈3 d) |
| **G2 SAML and sign-in** | #618, #619(1–3), #620 | `server.go` `buildSAMLMiddleware` + `SessionStore`, `sessionstore.go`, `domain/auth/saml_metadata.go`, `handler_admin_settings.go` (PATCH reload, security warnings), `handler_admin_sso.go`, `InsecureConfigBanner.tsx`, `domain/user/service.go` (`emailDomain` only), `cmd/server/main.go` (hourly ticker) | #635, #636 | M (≈1.5–2 d) |
| **G3 SLA race** | #623 | `queries/sla.sql`, `domain/sla/*`, `database/slastore` | none in code (#633 for DESIGN.md text) | S |
| **G4 ticket events** | #630 | `ticket/service.go` (`RemoveLink`, `UnassignForUser`, `DeleteGroup`, `CreateAttachment*`), `notification/*`, `notify/{webhook_body,format,staff}.go`, `handler_tickets.go` (`RemoveLink` handler), `WebhooksPage.tsx` `EVENTS` | #633, #636 | M (2 d) |
| **G5 follow-up limits** | #625 items 1–7 | `ticket/service.go` (`create`, `CreateFollowUp`, `refuseSecondFollowUp`), migration (link flag), `queries/tickets.sql` link queries, `queries/guest_tokens.sql`, `notify/{email,templates}.go` + template, `ticket_view.go` (`follow_up`), `requester_view.go`, `respond.go`, `TicketDetailPage.tsx`, `GuestTicketViewPage.tsx` | #633, #636, **G4** (`notification` codec) | L (≈3 d) |
| **G6 custom fields** | #626, #627, then #625 item 8 | `domain/customfield/*`, `customfieldstore`, `queries/custom_fields.sql`, `handler_tickets.go` (`handleCreateTicket`), `handler_guest.go` (`handleGuestCreateTicket`), `routes.go` guest block, `mcp/server.go` (`create_ticket`, `list_ticket_fields`), `GuestTicketPage.tsx`, (`CategoriesPage.tsx` + migration if D28 = flag) | #633, #638; **G5** before the #625.8 commit | L (≈4–5 d) |
| **G7 attachment visibility** | #628, #173 | new query in `queries/tickets.sql`, `ticketstore` (new method), `ticket_view.go` (`has_quarantined_attachment`), `mcp/server.go` (`list_attachments`), `TicketListPage.tsx` (badge), `AttachmentList.tsx` | #633, #636; #173 commit can start now | M (≈1.5 d) |
| **G8 credentials** | #622, #631 | `handler_admin_credentials.go`, `credentials_audit.go`, `authstore.go`, `queries/auth.sql`, `domain/auth/oauth.go`, `middleware/auth.go` (`BearerAuth`), `webhook_filter.go`, `webhook_source.go`, `OAuthClientsPage.tsx`, `WebhooksPage.tsx` (filter) | #635, #637 | M (≈2.5 d) |
| **G9 audit view** | #629 | `queries/audit.sql`, `auditstore`, `domain/audit/audit.go` (`SearchableKeys`), `handler_admin_audit.go`, `AdminAuditPage.tsx` | #633, #637 | M (≈2 d) |
| **G10 rate limits** | #621 | migration (counters), `queries/rate_limit.sql`, `queries/password_reset.sql`, new `ratelimitstore`, `passwordreset/*`, `handler_password_reset.go`, `server.go` (limiter fields), limiter calls in `handler_guest.go` / `handler_tickets.go` | #633, #635, #637 | M (2 d) |
| **G11 source-channel filter** | #624 (only if D18 = B) | `domain/ticket/filter.go`, `ListTicketsFiltered`, `handleListTickets`, `mcp/server.go` `buildListFilter`, `TicketListPage.tsx` (filter control) | #633, #636 | S–M (1 d) |
| **G12 real-environment** | #632, #619(4) | `AccountPage.tsx`, `.github/workflows/ci.yml`, checklist doc; manual runs | everything else (run the checklists on the release candidate) | S–M + ≈1 d manual |

**Shared files** (text conflicts only, resolved by the later PR):
- `docs/DESIGN.md`, `docs/upgrade-notes-1.3.0.md` and `docs/api/openapi.yaml`: everyone. Keep
  each PR's edits to its own sections.
- `internal/dbgen/*`: never hand-merged. The later PR rebases and runs `sqlc generate`.
- `server/server.go`: G1 (wiring), G2 (SAML builder), G8 (`AuthStoreIface`), G10 (limiters).
  These are different hunks.
- `cmd/server/main.go`: G1, G2, G6 and G10 wiring, plus the G2 ticker. G5's token sweep goes in
  the same hourly goroutine as G2's reload, so **G5 rebases onto G2** there.
- `mcp/server.go`: G6, G7 and G11, in different tools.
- `ticket_view.go`: G5 and G7, two separate fields.
- `TicketListPage.tsx`: G7 and G11.
- `WebhooksPage.tsx`: G4 (`EVENTS`) and G8 (filter).
- `domain/user/service.go`: G1 and G2, in different functions.
- `handler_tickets.go`: G4 (`RemoveLink`), G6 (create), G10 (on-behalf limiter) and G11 (list),
  in different functions.
- `handler_guest.go`: G6 (create) and G10 (limiter calls).
- `domain/audit/audit.go` `shownKeys`: G1 and G9.

**Migrations, in merge order:** G5 `000062_ticket_links_follow_up`, G10
`000063_rate_limit_counters`, G6 `000064_custom_field_guest_visible` (only if D28 = flag).
Renumber at PR time if the order changes or search has landed.

**Merge order:**
1. Wave 0: #633, #635, #636, #637, #638.
2. Start right away: G3 (code has no dependency) and G7's #173 commit (frontend only).
3. In parallel, after wave 0, merging in this order: **G3 → G4 → G2 → G1 → G8 → G9 → G7 →
   G11 → G10 → G5 → G6.**
   - G4 goes before G5 (codec), and G2 goes before G5 (`main.go` ticker).
   - G5 goes before G6's #625.8 commit.
   - G6 goes last among the code PRs because it is the largest and needs a security review of the
     guest route.
4. G12: the code part (button, CI job) can merge any time after wave 0. The manual checklists run
   on the release-candidate build, after everything else is merged.

**Total coding:** about 22–26 coder-days. With 4 coders in parallel, that is roughly 7–9
calendar days to merged, including review rounds. It does not include #616(a)/(c) if the owner
decides to build them (another 6–8 days). Every PR gets a pass/fail review before merge.

---

## 3. Owner decisions (with recommendations)

| # | Issue | Decision | Recommendation |
|---|---|---|---|
| D1 | #615 | End all of the user's sessions on an admin passkey removal, or only when it was the last factor? | **Always.** The key is presumed lost or compromised, and it is simpler. |
| D2 | #615 | Refuse an admin removing their **own** passkey through the admin route? | **Yes** (`409`, use the Account page). It closes a factor-guard bypass. |
| D3 | #615 | Also close the same gap for `reset_mfa` on oneself through `PATCH /admin/users/{self}` (found while reading, not in any issue)? | **Yes, in G1.** It is the same bypass and small. |
| D4 | #616(a) | Attestation: build an opt-in MDS3 policy (L, migration, network access), or document it as a consequence of accepting synced passkeys and take it out of the tag? | **Document, and move it out of the tag.** Enforcing it likely means "hardware keys only", which contradicts DESIGN.md. **This reverses the "fix before tag" decision for this part; the owner has to say so.** |
| D5 | #616(b) | On a sign-count clone warning: refuse sign-in and end sessions, or only flag it? | **Refuse, end sessions, write an audit entry.** Accept a small risk of locking out users with buggy counters. |
| D6 | #616(c) | Per-role "passkey required": build before the tag (L, high risk to the login state machine) or move it to 1.3.x? | **Move it.** If built: exempt SSO, and ignore the setting with a banner while passkeys are off. **This also reverses the "fix before tag" decision for this part.** |
| D7 | #617 | The losing request's answer. | `403 mfa_required` (the existing code, which the frontend already handles). |
| D8 | #618 | Reuse `spent_saml_handovers` for assertion IDs, or add a new table? | **Reuse** with a separate hash prefix. No migration, same meaning. |
| D9 | #619(2) | Also decode real UTF-16 bytes from a metadata **URL**? | **No.** Pasted text only; nobody has reported the URL case. |
| D10 | #619(3) | Make the settings PATCH reload OIDC too (same bug)? | **Yes**, in the same change. |
| D11 | #620 | Rewrite U-label entries to `xn--` at save, or keep refusing them with the computed form in the message? | **Keep refusing, and name the `xn--` form.** No silent rewriting. |
| D12 | #621 | Hybrid (durable counters for the named limits, forgot-password from existing rows), all limiters, or documentation only? | **Hybrid.** Documentation alone is not a fix. The login limiter's durable fix is planned separately. |
| D13 | #621 | Move `guestResendLimiter` too? | **Yes**; it costs one more key prefix. |
| D14 | #621 | Change from one shared window to a window per key (changes `Retry-After` semantics in DESIGN.md)? | **Yes.** It is stricter and more honest. |
| D15 | #622 | Replace the secret immediately, or allow a grace period with two valid secrets (migration, about 1 more day)? | **Immediate.** The main case is a leak. |
| D16 | #622 | Revoke outstanding access tokens on rotation (`sv` claim), and accept old tokens without it? | **Yes and yes.** |
| D17 | #623 | Should a response that came before a restart ever be recorded on the new clock? | **No.** The new clock starts at the reopen. |
| D18 | #624 | Fold into advanced search (not approved, about 9 days), or the minimal filter now (1 day, only the REST branch thrown away later)? | **Minimal now (G11)**, unless advanced search is approved and staffed before the tag. Staff and admin only; requesters get `403`. |
| D19 | #632, #619(4) | Who runs the manual checklists (Safari macOS/iOS, a hardware key, Apple Silicon Compose, Okta, Entra, Google Workspace), and with which tenants? Make the arm64 CI job required? | **The owner or a named person, on the release candidate.** List anything not run as "not verified" in the release notes. **Yes** to a required arm64 job, if the runner is available. |
| D20 | #625.1 | Wording of the guest follow-up mail. | "We have opened a new ticket to follow up on your earlier one. Ticket: {{.TrackingNumber}}…" |
| D21 | #625.3 | Should hand-made `parent_child` links stop using up the requester's follow-up (this reverses a pinned test)? | **Yes**, with existing links backfilled as follow-ups. |
| D22 | #625.4 | Deleted reporter: refuse with `409 reporter_deleted`, or file with the staff member as reporter? | **Refuse** with the clear code. |
| D23 | #625.6 | May a guest be told a follow-up exists? | **Yes, as a boolean.** |
| D24 | #625.8 | Copy custom-field values to a follow-up, and which ones? | **Yes.** Staff: every field that resolves. Requester: only `visible_on_new`. Not atomic; document it. |
| D25 | #626(a) | Enforce "required" on the API for every caller, staff on behalf included? **This is a breaking change** for integrations under CLAUDE.md's "No breaking changes". | **Enforce, including staff**, and list it under breaking changes in the upgrade notes. Ship it with #627. |
| D26 | #626(b,c) | Refuse blank and duplicate options and overlapping attachments from now on (rewriting a pinned test), and leave existing overlaps alone? | **Yes.** Both were already documented gaps. |
| D27 | #626(d) | Show retired option text to requesters too? | **Yes.** They already see the live options. |
| D28 | #627 | How guests opt in to fields: a per-attachment `visible_to_guests` flag (migration, L), or a global setting reusing `visible_on_new` (M)? | **The per-attachment flag**, default off. It is the only option where an operator chooses exactly what anonymous visitors see. |
| D29 | #627 | Return `warnings` to guests, or refuse bad values with `400`? | **`400`.** The endpoint is unauthenticated, and the form is ours. |
| D30 | #627 | MCP `get_ticket` returns custom-field values? | **Yes.** It is small, and an agent can read back what it wrote. |
| D31 | #628 | The quarantine marker in the list: staff only, or requesters too? | **Staff only**, like the SLA and Reopened markers. |
| D32 | #628 | Reputation verdicts over MCP? | **No** for 1.3.0. It would spend the operator's lookup quota from scripts. |
| D33 | #629(a) | Search the diff at all, given the cost? | **Yes**, on displayed keys only, after measuring on a large table. |
| D34 | #629(b) | Actor search inside `q`, or a separate parameter? | **Inside `q`.** Requesters are excluded while the mask is on. |
| D35 | #629(d) | Accept that the requester-name mask stays display-only (documented), with no code change? | **Yes.** It cannot be made real for administrators, who can open the ticket. **This means #629(d) is closed as documented, not fixed.** |
| D36 | #631 | How type and item combine with category: AND (as now) or OR? | **AND**, the documented rule. |
| D37 | #631 | Refuse contradictory filters (a type outside the ticked categories) with `400`? | **Yes**, following the existing "a hook that can never fire" rule. |
| D38 | #630 | New event names. | `ticket.unlinked` and `ticket.attachment_added`. Hooks set to `*` or to all events start receiving them; note it in the upgrade notes. |
| D39 | #630 | Mail on an attachment upload? | **No.** A file usually comes with a reply, which already mails. |
| D40 | #630 | Announce follow and unfollow? | **No.** Document it. It is personal, and announcing it would leak what the follow-audit setting hides. **So this part is closed as documented, not built.** |
| D41 | #630 | Bulk unassignments (user or group delete): announce to webhooks and suppress staff mail? | **Yes.** Also: should `RemoveLink` write an audit entry? **No**, for symmetry with `AddLink` (which writes none). |
| D42 | #173 | Marker wording. | "· shown in the summary". |

**Items that would not be "fixed" if the recommendations are taken:** #616(a) and #616(c) (D4, D6),
#629(d) (D35), follow and unfollow in #630 (D40), the per-address login limiter in #621 (D12),
and everything in #632/#619(4) that the manual runs do not cover (D19). Each needs the
owner's explicit decision before the tag, because the standing decision is "fix all of it".
