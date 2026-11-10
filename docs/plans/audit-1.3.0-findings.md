# 1.3.0 pre-tag audit: findings to fix

Source: four Fable corner-cutting audits of `v1.2.0..v1.3.0-beta` at `bc2f7a14`, plus
a sweep of deferred items, on 2026-11-09/10. The owner decided that **all of it is
fixed before the v1.3.0 tag**. Paths are relative to `backend/` unless they start with
`frontend/` or `docs/`. Line numbers are from `bc2f7a14` and may have moved.

Every fix is test-first, and the test must fail without the fix. Read
`docs/plans/agent-rules.md` on this branch for the process.

## Group AUTH (PR "auth-fixes")
- A1. OIDC callback reuses the previous browser session's flags (`MustChangePassword`,
  `PasswordChangeNeedsNoFactor`, `PendingMFASecret`, `PendingPasskey`, `PendingUntil`).
  `internal/server/handler_oidc.go:37-45, 236-242`. The fix is to build a fresh `auth.SessionData`,
  as `handleSAMLSession` does (`handler_auth.go:535-540`). Repro: flag a user with
  must_change_password, sign in with a password, then run OIDC on the same cookies, and `/me` answers 403 password_change_required.
- A2. Anonymous `GET /auth/oidc/login` writes a 7-day session row per request, with no throttle
  (`handler_oidc.go:45` → `writeSession`). Give pre-login sessions a short lifetime (about 10 min), and/or
  put the route behind `loginLimiter`. Test the row's expiry.
- A3. Test gap: MCP SSE session binding is checked by user only (`internal/mcp/server.go:521 ownedBy`).
  Add cases: a second API key of the same user, and the same user's browser session, posting to the stream; both expect 403.
  Check whether the code also needs a fix.
- A4. Test gap: the OIDC adopt-by-email refusals for a `saml_subject` that is set and for a different `oidc_subject`
  are enforced only by the fake store (`queries/users.sql:536`, `domain/user/service.go:953-967`).
  Add server-level cases beside `TestOIDCCallback_DoesNotLinkOntoAdmin`.
- A5. Test gap: SAML `FactorVerified` (`handler_auth.go:539`) has no session-level test. Add an e2e case for an
  assertion without MFA and one with it.
- A6. A half-signed-in session (MFA owed, or MustChangePassword) can `GET /api/v1/categories`
  (`internal/server/server.go:981-989 requireSignedInOrGuestsEnabled`). Require MFAPassed and
  !MustChangePassword on the signed-in branch, and add /categories to `TestHalfAuthenticatedSession_CannotReadReferenceData`.
- A7. Test gap: TOTP confirm re-check versus a passkey registered between stage and confirm
  (`handler_me.go:300`).
- A8. Test gap: the `POST /auth/reset-password` rate limit (`handler_password_reset.go:117`).
- A9. Test gap: `POST /auth/oauth/token` refusals (wrong secret, unknown client, wrong grant). Use
  `subtle.ConstantTimeCompare` for the secret check (`handler_auth.go:319-353`).
- A10. API key `last_used_at` is never written, though the UI and OpenAPI show it. Wire a detached,
  throttled `UpdateLastUsed` in `middleware/auth.go APIKeyAuth` (like the passkey `Touch`).
- A11. OIDC token exchange runs with no timeout (`oidc.go:96` comment is false). Use
  `oidc.ClientContext(ctx, &http.Client{Timeout: oidcFetchTimeout})` in the callback. Delete the false
  sentence about internal addresses in `domain/auth/saml.go:20-24`.
- A12. OAuth client scopes are not limited to the service user's role (`handler_admin_credentials.go:426`).
  Apply `WithinRole` as for keys.

## Group AUDIT-ENTRIES (PR "admin-audit-entries")
- B1. Create, update and delete of OAuth clients (`handler_admin_credentials.go:408-477`) and of webhooks
  write audit entries in the same transaction, matching `api_key_issued`/`api_key_revoked`. Secrets never
  go into the audit entry. The owner approved this. Update the audit view's shownKeys and filter, and DESIGN.md.

## Group INTEGRATIONS (PR "integration-fixes")
- C1. The webhook client follows redirects, so a 301/302 is recorded as ok with an empty GET
  (`internal/safehttp/safehttp.go:35-44`, `notify/webhook.go:194, 686-704`). Set `CheckRedirect` to
  `http.ErrUseLastResponse`, so a 3xx is recorded with its real code. Test it.
- C2. An attachment upload extends only the read deadline, so `WriteTimeout` 30s kills slow uploads after
  commit and a retry creates a duplicate (`handler_attachments.go:635`; the comment at :40-55 is false). Add
  `SetWriteDeadline` and a slow-body test.
- C3. `PATCH /admin/settings` doesn't type-check `mfa_enabled`, `ticket_scope_enforced`,
  `sla_enabled`, `self_signup_enabled`, `open_registration_enabled`, `saml_enabled`,
  `reopen_window_days` (also refuse values < 0), `mfa_enforced_roles`, `reopen_target_status_name`, or
  `site_logo_url` (refuse non-http(s)) (`handler_admin_settings.go:241-747`). Run every known key
  through `unmarshalSetting`.
- C4. MCP: `buildListFilter` errors are not wrapped in `ticket.ErrValidation`; `ErrStatusInactive`,
  `ErrStatusNotFound` and `ErrForbidden` aren't passed through `storeErr`
  (`internal/mcp/server.go:884-934, 1204-1217`). Don't log caller mistakes at ERROR. Test end to end.
- C5. MCP `add_reply` hard-codes a 7-day reopen window (`internal/mcp/server.go:856`); read the setting.
- C6. MCP `list_categories` with `include_inactive` has no role check, unlike REST; match REST.
- C7. SMTP: a user name without a password and without STARTTLS sends AUTH in the clear
  (`notify/email.go:834`). Treat a user name like a password, and add a row to `TestSendMail_TLSRule`.
- C8. TOCTOU on the joined-HTML size cap when saving a template (`handler_admin_email_templates.go:286-293`):
  re-read the `_header`/`_footer` rows inside the transaction.
- C9. Redelivering on a *paused* hook says "disabled"; use a `webhook_paused` code or wording that names re-approval
  (`webhook.go:507`, `handler_admin_webhook_history.go:74-75`).
- C10. `safehttp.ValidateURL` uses context-less `net.LookupIP` (`safehttp.go:139`); use `LookupIPAddr(ctx, …)`.
- C11. Test gaps: staff reopen mail has no HTTP test; the HTTP harness swallows sink errors
  (`internal/testutil/txrunner.go:53`). Fail on a sink error, and add one test per reopen door.
- C12. Test gap: the transactional guest per-ticket attachment cap (`domain/ticket/service.go:3071-3085`). Add a
  direct or concurrent test at the cap.
- C13. Dead code: `domain/attachment/detect.go:19-22 errNotImplemented`. The always-skipped fixture is
  `notify/email_html_round3_test.go:36`; assert the refusal instead. A weak assertion uses `"status"` instead of
  `status_id` in `protect_mcp_scope_test.go:132` and `internal/mcp/scope_test.go`.
- C14. The DESIGN.md outbox lease says 300 s at :4099, and the `outbox.go:142` comment says the same; the code uses 10 min. Fix the text.

## Group DATA (PR "data-test-fixes")
- D1. `internal/database/custom_field_locks_test.go:138-295`: a failing stage hangs until the 10-minute timeout.
  Give every channel receive a bounded wait.
- D2. Auto-assignment does not run for guest tickets (`POST /guest/tickets`) or for MCP `create_ticket` and
  `create_follow_up` (DESIGN.md:695; PR #363 called the guest case "likely a bug"). Make every creation door
  auto-assign the same way. Check DESIGN.md first, and update it.
- D3. Reclassifying a ticket (UpdateCTI) does not re-match the SLA policy and writes no audit entry
  (PR #468; DESIGN.md §8). Re-match, and audit in the same transaction.

## Group FRONTEND (PR "frontend-test-fixes")
- E1. Nothing tests the Markdown worker's 1 s time budget (`frontend/src/lib/markdownBackend.ts:9, 70-92`).
  Add an e2e test that forces an overrun and asserts "too complex to format", the worker terminated, and the next post formats.
- E2. `frontend/src/components/editor/editorCore.ts:29-37` claims a test catches dropped preset parts. Add
  that test (assert `editorPlugins` contains none of the dropped identities).
- E3. `frontend/playwright.config.ts:27` sets `retries: 1` on CI with no reason. Set it to 0, or keep it and fail on flaky.
- E4. `CategoriesPage.tsx` field-assignment add/toggle mutations show no error (pre-existing). Show it.

## Untracked limits: the owner decided all are fixed before the tag
These need a design decision each. Every one is first filed as a 1.3.x issue, then designed and built.
See the 1.3.x issues titled "[pre-tag] …". The list is in the issues; don't build from this file.
