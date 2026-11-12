# Release-candidate checklists, 1.3.0

Hand-run checks for what automated tests cannot cover. Erik runs them (decision D19) against the
release-candidate image. Source: issue #632 (never checked in a real environment) and `docs/DESIGN.md`
(Authentication: Passkeys, SAML, OIDC, Identity provider lockout guard). Every label, route and setting
name below was checked against `origin/v1.3.0-beta` at `6a1ca799`.

## How to use this

1. Run every list against the **release-candidate image**, not a development build. Set `GHD_VERSION` to the
   candidate's tag in `docker/.env`.
2. Start each step line with `[ ]`. Replace it with `[P]` (pass), `[F]` (fail) or `[N]` (not run). On `[F]`,
   write what you saw under the step: the exact message, the status code, a screenshot name.
3. A step marked **Expect** has one right answer. If what you see differs in any way, that is a fail, even if
   it looks harmless.
4. Anything left `[N]` goes into the release notes as **"not verified"**, with the list and step number, for
   example "Passkeys on iOS Safari: not verified". Do not drop it silently.
5. The result sheet at the end collects each list's verdict and gives the release-notes wording.
6. A `[F]` on a negative case (a refusal that did not happen) is more serious than a `[F]` on a happy path.
   Stop and report it before tagging.

### Things to know before you start

- **Safari may refuse a passkey prompt that does not follow a click closely enough.** The page asks the server
  for a challenge first and only then asks the browser (`signInWithPasskey` and `addPasskey` in
  `frontend/src/api/passkeys.ts`). Nobody has tried this in Safari. If a sheet does not appear after a click,
  that is a finding, not a mistake in how you ran the list.
- **Known open risk (#632, PR #342).** On the Account page, after you verify your existing factor, the page
  re-runs "add passkey" by itself with no new click (`StepUpPanel` in `AccountPage.tsx`). The planned change to
  an explicit button is **not** in `v1.3.0-beta`. Safari may decline that second prompt. If the browser says
  "not allowed", the page treats it as you cancelling: no message, the panel closes, no key is added. List 1
  step 1.9 tests this on purpose.
- **Changing `BASE_URL` invalidates every registered passkey.** Pick the HTTPS name once, before list 1, and
  keep it for all six lists.

## Shared setup (once, before list 1)

- **S1. A throwaway stack.** Copy the `docker/` directory somewhere new so its `./data/postgres` is empty.
  Give it its own project name (`docker compose -p rc130 ...`) so it cannot touch another stack. In
  `docker/.env` set `SESSION_SECRET` (32 or more characters), `GHD_VERSION` (the candidate) and `BASE_URL`.
  `docker-compose.yml` publishes port 8080; point your HTTPS front end at it.
- **S2. A real HTTPS name for `BASE_URL`**, for example `https://rc.example.com`, reachable from your Mac,
  your iPhone, and the identity providers' redirect pages. Passkeys need a domain name (an IP address, a
  one-word host or a non-ASCII host switches passkeys off, see #494). `localhost` would do for list 1 alone,
  but not for the phone or for any SSO list, and switching later breaks every passkey. A reverse proxy or a
  tunnel both work. SAML's tracking cookie is only set the way the code intends over HTTPS (`SameSite=None;
  Secure`), so test SAML over HTTPS too.
- **S3. First administrator.** Open `/setup` and create the administrator **A** (call the address
  `admin@...`). A is the **only** administrator for the whole run, which is what makes the "last
  administrator" checks meaningful. Sign in and confirm there is no red banner under Admin (a banner about
  `BASE_URL` means passkeys are off).
- **S4. Test users** (Admin, Users, **Add User**: Email, Display name, Role, Password): a staff user **K**
  (`kay@...`) with a password you know.
- **S5. Two ways in to the server, both from the `docker/` directory:**
  - database: `docker compose -p rc130 exec db psql -U helpdesk helpdesk`
  - server command: `docker compose -p rc130 exec app /app/server reset-factors <email>`
- **S6. Settings you will touch** all live at Admin, **Settings**, tab **Authentication**: section **SAML**
  (toggle *Enable SAML login*), section **OIDC / OpenID Connect**, section **Multi-factor authentication**
  (*Enable MFA*, *Require MFA for roles*), section **Registration** (*Allow self-service signup*, *Allowed
  email domains*). The page's own **Save changes** button at the bottom saves the toggles; SAML and OIDC
  have their own save buttons inside their boxes.
- **S7. Browsers.** Mac: Safari (current) with a normal window, plus one second window or profile for
  "another session". Turn on Safari's Develop menu (Settings, Advanced) for the Web Inspector. iPhone:
  current iOS Safari. Both signed in to the same Apple ID with iCloud Keychain on.

---

## List 1. Passkeys in Safari on macOS

**Why:** the only passkey tests today are in a simulated browser (jsdom). Nothing has run in a real
Safari with a real authenticator.

### Prerequisites

- Shared setup S1 to S7 done. Safari on the Mac, Touch ID available (or a paired Apple Watch).
- Administrator A and staff user K exist. No passkeys registered yet.
- Admin, Settings, Authentication, **Multi-factor authentication**: *Enable MFA* **off** for now. Leave all
  *Require MFA for roles* boxes unticked. Save changes.

### Steps

- [ ] **1.1** In Safari, sign in as K. Click **Your account**. **Expect:** a **Passkeys** box saying "No
  passkeys yet", a field **Name this key** and a button **Add a passkey**. (If it says "This browser does not
  support passkeys" or "Passkeys are not available on this instance", stop: that is a finding.)
- [ ] **1.2** Type `Mac Touch ID` in **Name this key**, click **Add a passkey**. **Expect:** Safari's passkey
  sheet appears straight after the click (see "Things to know"). Approve with Touch ID. The list shows
  `Mac Touch ID`, a **Synced** badge (iCloud Keychain passkeys can sync, so the server marks them
  backup-eligible; with iCloud Keychain off on the Mac it may read **This device only**, so note which),
  "Added <today> · never used". *Why:* proves registration end to end, and that the badge is right.
- [ ] **1.3** Click **Add a passkey** again with an empty name. **Expect:** Safari does not offer to create a
  second passkey for the same account on this device (the server excludes keys already registered), or the
  page shows a clear message. Record which. Nothing should be added twice.
- [ ] **1.4** Click **Add a passkey**, then dismiss the sheet. **Expect:** no red error and no change (a
  dismissed prompt is treated as a choice).
- [ ] **1.5** Turn MFA on: as A in a second window, Settings, Authentication, tick *Enable MFA*, Save
  changes. Leave *Require MFA for roles* unticked. **Expect:** saved.
- [ ] **1.6** In Safari, **Sign out**, then sign in as K with the password. **Expect:** the title becomes
  "Confirm it is you" and a button **Continue with a passkey**. *Why:* this account's only second factor is a
  passkey, and `passkey_needed` must be what drives this screen.
- [ ] **1.7** Before approving anything, try to open `/dashboard` and `/tickets` directly in the address bar.
  **Expect:** you do not see ticket data (an error or a bounce back to sign-in). *Why:* a password alone must
  not be enough once a passkey is registered.
- [ ] **1.8** Back on the sign-in tab (sign in again if you were bounced), click **Continue with a
  passkey**, approve with Touch ID. **Expect:** you land on the dashboard as K. In the other window, A opens
  Admin, Users, K: **MFA** shows **Enrolled**, and the passkey line shows `Mac Touch ID`, "Synced", "last
  used <today>".
- [ ] **1.9** **The Safari retry test.** As A, turn *Enable MFA* **off** and Save changes. As K, sign out and
  in with the password only (you are let straight in, no passkey step). Open **Your account**, type
  `Second key` and click **Add a passkey**. **Expect:** a panel **Verify your second factor** appears with a
  **Verify with a passkey** button. Click it and approve with Touch ID. Then watch what happens without
  clicking anything:
  - **Best case:** a second Safari sheet appears at once; approve it; `Second key` is added. Record "retry
    worked".
  - **Known risk:** no second sheet; the panel closes; nothing is added and no message shows. Record "Safari
    declined the automatic retry". Then click **Add a passkey** once more. **Expect:** the sheet appears now
    and the key is added (your session has proved the factor). Record this as a fail of the automatic retry
    but a pass of the recovery, and note it for the release notes.
  *Why:* this is the one concrete Safari hazard written down in #632.
- [ ] **1.10** Turn *Enable MFA* back **on**. As K, remove `Second key`: trash icon, then **Remove** in the
  "Remove this passkey?" box. **Expect:** it disappears and `Mac Touch ID` stays. With a second browser
  window signed in as K beforehand, that other window is signed out on its next click (removing your own key
  ends your other sessions and keeps this one).

### Negative cases

- [ ] **1.11 Cancelled prompt.** Sign out, password step, click **Continue with a passkey**, dismiss the
  sheet. **Expect:** still on "Confirm it is you", no red error, button usable again.
- [ ] **1.12 Half-signed-in session expires.** Password step, then wait **6 minutes** without touching the
  passkey. Click **Continue with a passkey**. **Expect:** an error appears and you are not signed in; reloading
  the page and signing in again with the password works. *Why:* a password-accepted session is only good for 5 minutes (#452).
- [ ] **1.13 Replay is refused.** Open Safari Web Inspector, Network, tick *Preserve Log*. Sign out and in
  again with the passkey. Right-click the `POST .../auth/local/passkey/finish` request, choose **Copy as
  cURL**, and run it in Terminal after signing out. **Expect:** not 200 or 204; a 400 (`no_challenge`) or 401.
  *Why:* a captured sign-in must not work twice. If Safari's copy lacks the cookie header, mark `[N]`: a
  refusal without the cookie proves nothing.
- [ ] **1.14 Cannot reset your own.** As A (MFA on), **Your account**, name `Admin Mac`, **Add a passkey**.
  Then open Admin, Users, A's own row. **Expect:** no **Reset MFA** button and no passkey Remove buttons; a
  line saying to manage your own on the Account page. *Why:* the server refuses both on your own account
  (`409 own_account`), so one administrator cannot strip their own factor around the guard.
- [ ] **1.15 Last administrator can still recover.** Sign out. In macOS Passwords (System Settings,
  Passwords), delete the passkey for `admin@...` (this simulates a lost key; leave K's). Sign in as A:
  password, then **Continue with a passkey**. **Expect:** it fails (no passkey to offer). Run
  `reset-factors admin@...` (S5). **Expect:** the command succeeds; sign in with the password and reach the
  dashboard (or the enrolment step, if MFA is enforced for administrators). *Why:* A is the only
  administrator, so nobody else can remove the key; this command is the only way back.

### Cleanup

- Safari, Passwords: delete every passkey for the `BASE_URL` host (they sync through iCloud Keychain to the
  iPhone, which would confuse list 2). Check the iPhone's Passwords app too after it syncs.
- Remove the K and A passkeys from the help desk if you are reusing the stack; otherwise discard the stack
  (see the end).

---

## List 2. Passkeys on iOS Safari, including an iCloud Keychain passkey

**Why:** iOS Safari has its own sheet (Face ID or Touch ID) and its own gesture rules. A synced passkey also
reports a sign count of zero forever, which must not be mistaken for a copied key.

### Prerequisites

- List 1 cleanup done (no old passkeys for this host).
- iPhone on current iOS, Face ID or Touch ID on, iCloud Keychain on, the same Apple ID as the Mac. The phone
  reaches `BASE_URL` over HTTPS with a certificate it trusts.
- Settings, Authentication, *Enable MFA* **on**, no roles enforced. Staff user K exists with a password.
- A database shell open (S5).

### Steps

- [ ] **2.1** On the iPhone, sign in as K (password; MFA is on but K has no factor yet, so you go straight
  in). Open **Your account** (the navigation may be behind a menu button on a phone).
- [ ] **2.2** Name it `iPhone Face ID`, tap **Add a passkey**. **Expect:** the iOS passkey sheet appears
  right after the tap; approve with Face ID. The list shows `iPhone Face ID` with **Synced**.
- [ ] **2.3** Sign out. Sign in with the password. **Expect:** "Confirm it is you" with **Continue with a
  passkey**. Tap it, approve with Face ID. **Expect:** you reach the dashboard. Repeat sign-out and sign-in
  twice more (three sign-ins in total).
- [ ] **2.4** In the database shell:
  `SELECT name, sign_count, backup_eligible, backup_state FROM webauthn_credentials;` Wait 10 seconds after
  the last sign-in first (the counter is saved just after the response). **Expect:** `backup_eligible` is
  true. Note `sign_count`. If it is 0, that is normal for a synced passkey; if it rises, also fine.
  **Expect in both cases:** none of the three sign-ins was refused.
- [ ] **2.5 The synced passkey travels.** On the Mac (Safari, with iCloud Keychain on), wait a minute for sync,
  sign in as K, password step, **Continue with a passkey**. **Expect:** Safari offers the `iPhone Face ID`
  passkey (it may ask to use the iPhone or show it as an iCloud Keychain passkey) and you sign in without
  registering anything on the Mac. Record how long the sync took and what the sheet looked like. If the Mac
  does not offer it, record `[N]` with the reason (for example, sync was slow); do not call it a help desk
  failure.
- [ ] **2.6 The iOS retry test (same hazard as list 1, step 1.9).** Turn *Enable MFA* **off**. On the iPhone,
  sign out and in with the password only. Account, name `Phone second`, **Add a passkey**. **Expect:** the
  **Verify your second factor** panel; tap **Verify with a passkey**, approve. Then watch for a second sheet
  with no tap, as in 1.9. Record "retry worked" or "iOS declined the automatic retry", and whether tapping
  **Add a passkey** again then worked.
- [ ] **2.7** Turn MFA back **on**. Remove `Phone second` from the Account page on the iPhone. **Expect:** the
  confirmation box "Remove this passkey?", then the key is gone and `iPhone Face ID` stays.

### Negative cases

- [ ] **2.8 Cancel.** Password step, tap **Continue with a passkey**, cancel Face ID. **Expect:** no red
  error; you can tap again.
- [ ] **2.9 Face ID failure.** Password step, tap **Continue with a passkey**, fail Face ID until the sheet
  gives up. **Expect:** no session, no error that reveals anything beyond "not allowed" or similar; a later
  attempt works.
- [ ] **2.10 Wrong site.** Open the iPhone's Passwords app and confirm the passkey is listed under the exact
  host of `BASE_URL`. Then visit the same help desk through a different host name if you have one (for
  example a second hostname pointing at the same server). **Expect:** the passkey is not offered there. *Why:*
  a passkey is tied to the host it was made for; this is the phishing-resistance claim in the design. Mark
  `[N]` if you have no second hostname.
- [ ] **2.11 A passkey is not a way in by itself.** On the iPhone sign in as K, then sign out. At the
  password step enter a **wrong** password. **Expect:** refused at the password; the passkey button never
  appears. *Why:* a passkey here is only a second step after the password, never a replacement.

### Cleanup

- Passwords app on the iPhone and Mac: delete the passkeys for the `BASE_URL` host (iCloud syncs the deletion;
  check both devices).
- In the help desk, Admin, Users, K and A: remove any remaining keys, or discard the stack.

---

## List 3. A hardware security key (for example a YubiKey), sign-count check, administrator removing one key

**Why:** a hardware key reports a real, rising counter. The server uses it to refuse a copied key
(`passkey_suspect`). The sign-count rule, the audit entries and the admin removal have only been tested
with fake keys.

### Prerequisites

- One hardware security key (USB or NFC) and, to run the removal steps, a **second** kind of key for the
  same user: use the Mac's Touch ID passkey from list 1 or a second security key.
- Settings, Authentication, *Enable MFA* **on**, no roles enforced.
- Staff user K exists. Administrator A is signed in in a separate browser profile (so you can switch between
  K and A without signing either out).
- Database shell open.
- Safari on the Mac for the main run. Optionally Chrome too; note which browser each step used.

### Steps

- [ ] **3.1** As K (Safari), **Your account**, name `YubiKey`, **Add a passkey**. In Safari's sheet choose the
  security key option, insert or tap the key, touch it (enter its PIN if asked). **Expect:** `YubiKey` listed
  with **This device only** (a hardware key is not backup-eligible). Record whether it asked for a PIN.
- [ ] **3.2** Sign out, password, **Continue with a passkey**, touch the key. **Expect:** signed in. Repeat
  twice (three sign-ins).
- [ ] **3.3 The counter rises.** After 10 seconds: `SELECT name, sign_count, backup_eligible FROM
  webauthn_credentials WHERE name = 'YubiKey';` **Expect:** `backup_eligible` is false and `sign_count` is
  above 0 and higher than after one sign-in. If it stays 0, this key reports zero forever and 3.4 does not
  apply; record that and mark 3.4 `[N]`.
- [ ] **3.4 A copied key is refused.** Simulate a copy that has been used elsewhere by pushing the stored
  counter far past the key's: `UPDATE webauthn_credentials SET sign_count = 2000000000 WHERE name =
  'YubiKey';` Keep K signed in in a second window. In the first window sign out, password, **Continue with a
  passkey**, touch the key. **Expect:** refused with the message "that key's signature counter did not
  advance, which can mean it has been copied. You have been signed out..."; **K's other window is signed out
  too** (every session ends); the stored `sign_count` is still 2000000000 (not lowered). As A, open Admin,
  **Audit Log**, filter Action `passkey_clone_suspected`: **Expect:** an entry on K's account, with no actor
  named.
- [ ] **3.5 Recovery from a refused key (staff).** K cannot sign in with that key now. As A: Admin, Users, K,
  the **Passkeys** list, trash icon beside `YubiKey`, **Remove**. **Expect:** the box says the key will stop
  working and K will be signed out everywhere, and that other keys are not touched. After confirming, the key
  disappears. Audit Log, Action `passkey_removed_by_admin`: **Expect:** an entry naming A and the key's id,
  not its label.
- [ ] **3.6** As K: password sign-in now. K has no factor registered (if `YubiKey` was the only one) so MFA
  does not ask for a key. Add `YubiKey` again (3.1). **Expect:** it registers as a new key with a fresh
  counter and signs in normally. *Why:* "remove and register again" is the documented remedy.

### Removing one of several keys (admin)

- [ ] **3.7** As K, make sure K holds **two** keys: `YubiKey` and `Mac Touch ID` (add it if needed). Open a
  second K session in another browser window.
- [ ] **3.8** As A: Admin, Users, K. **Expect:** a **Passkeys** list with both keys, each with its Synced or
  This device only line. Remove `YubiKey` only. **Expect:** K's two sessions end (K is signed out
  everywhere); `Mac Touch ID` is untouched; the audit entry `passkey_removed_by_admin` names that key.
- [ ] **3.9** As K: sign in with the password, **Continue with a passkey**. **Expect:** Safari offers only
  `Mac Touch ID`. Touching the YubiKey does nothing or Safari ignores it. The removed key cannot sign in.

### Negative cases

- [ ] **3.10 An unregistered key is refused.** If you have a second security key not registered to K, use it at
  K's passkey step. **Expect:** it does not sign K in (the browser may say it is not recognised or time out).
  Mark `[N]` if you have no spare key.
- [ ] **3.11 The administrator's own lost key.** As A, Account, add `YubiKey` (A is the sole administrator).
  Sign out, password step, unplug the key. **Expect:** at "Confirm it is you" nothing you do signs A in. Run
  `reset-factors admin@...` (S5). **Expect:** it succeeds; sign in with the password and reach the app.
  *Why:* the last administrator must still be able to recover.
- [ ] **3.12 The key works again after the reset.** After 3.11, re-add the key to A and sign out
  and in once with it. **Expect:** works. (Confirms `reset-factors` left no stale state.)

### Cleanup

- Reset the YubiKey's stored passkeys if the browser stored a discoverable one on it (Yubico Authenticator,
  Passkeys). Whether your browser stores one on the key depends on what it chooses at registration; if the
  list is empty, nothing to do.
- Delete K's and A's keys in the help desk, or discard the stack.

---

## List 4. Okta, by SAML and by OIDC

**Why:** a signed assertion from a fake IdP is tested end to end already
(`TestSAMLSignIn_EndToEndWithASignedAssertion`). What nobody has run is a real Okta sending a real
assertion, and a real replay against it.

### Prerequisites

- An Okta Developer org (free) with five users, each with an email address and a name: **U1** (first
  SAML sign-in), **U2** (email **exactly A's address**), **U3** (an email in a different domain from the
  others), **U4** (first OIDC sign-in) and **U5** (email matching a local staff account you create in 4.18).
  Assign them to each app below as the steps say.
- Shared setup done. Administrator A is the only administrator. Staff user K exists.
- A certificate and key for the help desk (the **SP certificate and private key**). Any pair that parses
  works; for example:
  `openssl req -x509 -newkey rsa:2048 -nodes -keyout sp.key -out sp.crt -days 30 -subj "/CN=helpdesk-rc"`
- Settings to use, Admin, Settings, Authentication: **SAML** box and **OIDC / OpenID Connect** box.

### Part A. SAML

- [ ] **4.1 Order matters.** The help desk only serves its own metadata after it has loaded the IdP's. So first
  create the Okta app. In Okta: Applications, Create App Integration, **SAML 2.0**.
  - **Single sign-on URL**: `{BASE_URL}/api/v1/auth/saml/acs`
  - **Audience URI (SP Entity ID)**: `{BASE_URL}/api/v1/auth/saml/metadata`
  - **Name ID format**: EmailAddress; **Application username**: Email
  - Attribute statements: `email` = `user.email`, `displayName` = `user.displayName`
  Assign U1 and U2 (U3 later, in 4.12). On the app's Sign On tab copy the **metadata URL**.
- [ ] **4.2** In the help desk as A: toggle **Enable SAML login** on (it reveals the SAML box). Choose **URL**,
  paste the Okta metadata URL as **IdP metadata URL**. Under **SP certificate (PEM)** upload `sp.crt` and
  under **SP private key (PEM)** upload `sp.key`. Click **Save SAML config**. **Expect:** "SAML config saved."
  and the badge **Configured**, with an **SP metadata** URL beside it. (An amber warning "metadata could not be
  loaded" means the Okta URL is wrong or unreachable.) Do **not** press the page's **Save changes** yet:
  leave *Enable SAML login* effectively off for now so passwords keep working for everyone.
- [ ] **4.3** Open the **SP metadata** URL in a browser. **Expect:** XML. Read the `entityID` and the
  `AssertionConsumerService` `Location`. **Expect:** `Location` is `{BASE_URL}/api/v1/auth/saml/acs`. If
  `entityID` is not exactly what you gave Okta, fix it in Okta; this decides whether Okta accepts requests.
- [ ] **4.4** Sign out. **Expect:** the sign-in page now shows a **Sign in with SAML** button. Click it.
  **Expect:** Okta's sign-in page. Sign in as U1. **Expect:** back at the help desk, signed in as U1, on the
  dashboard. Admin, Users, U1: role **User**, email and name as in Okta, **Login type** shows **SSO (SAML)**.
- [ ] **4.5** Sign out, **Sign in with SAML** again. **Expect:** straight in (Okta remembers you), and Admin,
  Users still shows one U1, not two.
- [ ] **4.6 Name refresh.** Change U1's display name in Okta, sign out and sign in with SAML. **Expect:** the
  new name shows (each sign-in of a known person rewrites the stored name).
- [ ] **4.7 SSO counts as MFA.** As A tick *Enable MFA* and *Require MFA for roles*: **User**, Save changes.
  Sign U1 out, sign in with SAML. **Expect:** straight in with no code prompt and no enrolment screen. As a
  control, sign in locally as a new local role-User account (create one in Admin, Users): **Expect:** the
  password sign-in is sent to set up an authenticator. Then untick the roles and Save.

#### Negative cases (SAML, Okta)

- [ ] **4.8 IdP-initiated sign-in is refused.** In Okta, sign in as U1 on the end-user dashboard and click the
  help desk tile. **Expect:** you are not signed in; a plain error page (403). *Why:* only a request the help
  desk started is accepted, which is also what stops an unsolicited assertion.
- [ ] **4.9 Replay of the assertion is refused.** Use Chrome or Safari with Network logging and *Preserve
  Log*. Sign out, sign in with SAML. Find the `POST .../auth/saml/acs` request. **Copy as cURL** (it must
  include the `Cookie` header with a `saml_` cookie). Run it in Terminal. **Expect:** `403`, not a redirect.
  *Why it matters:* without a recorded-assertion check, the same signed response could be sent again for about
  90 seconds. A cURL missing the cookie is refused for the wrong reason: mark `[N]` and retry.
- [ ] **4.10 Replay of the hand-over is refused.** In the same log, **Copy as cURL** on the
  `GET .../auth/saml/complete` request (with its `token` cookie). Sign out, then run it. **Expect:** a `303`
  redirect to `/login?error=sso_session_used`; opened in a browser, the page says "That sign-in has already
  been used. Please sign in again."
- [ ] **4.11 An email held by an administrator is never linked.** Sign out, sign in with SAML as **U2** (same
  email as A). **Expect:** back on the login page with "Another account here already uses the email address
  your identity provider sent. Ask an administrator to sort it out." As A, check Admin, Users: A is still an
  **Administrator**, **Login type Local**, and no SAML subject was attached. *Why:* SAML never adopts an
  existing account by email, so a colleague's identity cannot take over an admin.
- [ ] **4.12 Domain list gates new people only.** (The list is only shown while *Allow self-service signup* is on, and that
  needs email set up: follow the prerequisites and step 6.6 of list 6.) Set **Allowed email domains** to a domain that U1 is not in. Assign **U3** in Okta
  and sign in as U3 with SAML. **Expect:** "Accounts from your email domain cannot sign in here." Sign U1 out
  and in again. **Expect:** U1 still signs in (a known SAML person is not re-checked against the list). Remove
  the list afterwards.
- [ ] **4.13 Administrator failsafe with SAML-only mode.** As A, make sure K has a local password. Tick
  *Enable SAML login* on the page and click the page's **Save changes**. Sign out. Sign in as K with the
  password. **Expect:** refused with "local login is disabled; use SAML". Sign in as A with the password.
  **Expect:** works. *Why:* administrators keep password login so the instance cannot lock itself out.
- [ ] **4.14 Broken IdP does not lock out the sole administrator.** As A, change **IdP metadata URL** to a URL
  that fails (for example add `x` to the host) and click **Save SAML config**. **Expect:** a warning "SAML config saved, but the
  identity provider metadata could not be loaded..." (or a refusal), and **A can still sign in with the
  password**. Record whether **Sign in with SAML** still works afterwards (the running settings should be
  kept). Restore the right URL and save.

### Part B. OIDC

- [ ] **4.15** In Okta: Create App Integration, **OIDC**, **Web Application**. **Sign-in redirect URI:**
  `{BASE_URL}/api/v1/auth/oidc/callback`. Assign U1, U2, U4 and U5. Copy the **Client ID** and **Client secret**. The
  **issuer** is your Okta org URL (`https://<org>.okta.com`) or its `/oauth2/default` form; use the one your
  org's discovery document (`<issuer>/.well-known/openid-configuration`) answers on.
- [ ] **4.16** In the help desk as A: in the **OIDC / OpenID Connect** box set **Enable OIDC login** on,
  **Issuer URL**, **Client ID**, **Client secret**, click **Save changes** (the one in the OIDC box).
  **Expect:** "OIDC config saved.", badge **Configured**, and a **Redirect URL** shown: exactly the callback
  from 4.15. (If they differ, fix Okta.)
- [ ] **4.17** Sign out. **Expect:** a **Sign in with OIDC** button. Sign in as **U4**. **Expect:** signed in, new account with role **User**, **Login type** **SSO (OIDC)**.
- [ ] **4.18 A local account is adopted.** In Admin, Users, **Add User**: create a staff account whose email is
  **U5**'s. Sign in with OIDC as U5. **Expect:** you are signed in as that **same** staff account (no
  second row), **Login type** **Local + SSO**.

#### Negative cases (OIDC, Okta)

- [ ] **4.19 Not linked onto an administrator.** Sign in with OIDC as **U2** (A's email). **Expect:** "Your
  single sign-on identity could not be linked to the existing account with that email address. Ask an
  administrator." A is unchanged.
- [ ] **4.20 A different identity for the same email is refused.** In the database set a made-up subject on a
  non-administrator that U5 maps to: `UPDATE users SET oidc_subject = 'someone-else' WHERE email = '<U5
  email>';` Sign in with OIDC as U5. **Expect:** the same "could not be linked" message and no session.
  Delete that test account afterwards.
- [ ] **4.21 A SAML account is not adopted by OIDC.** Sign in with OIDC as **U1** (who signed in through SAML
  in 4.4). **Expect:** "could not be linked" (an account that came through SAML is refused). *Why:* proves the
  two providers do not silently merge identities.
- [ ] **4.22 A used or stale callback is refused.** Sign in with OIDC, sign out, then paste the callback URL
  (with `code` and `state`) from the browser history into the address bar. **Expect:** back on the login page
  with "Your sign-in expired or was started in another window. Please try again." *Why:* the login state is
  cleared on use, so a saved URL does nothing.
- [ ] **4.23 A wrong client secret fails cleanly.** Save a wrong **Client secret**, try to sign in. **Expect:**
  no session and one of the login-page messages (the exact one is not specified; record it). Restore the
  right secret. (Leaving the box blank on save keeps the stored one.)
- [ ] **4.24 Last administrator.** Switch OIDC off (**Enable OIDC login** off, save). **Expect:** accepted, and
  A still signs in with the password. *Why:* the guard refuses a change that would leave every administrator
  with no way in; here A has a password, so it must be allowed.

### Cleanup

- Okta: delete the SAML and OIDC app integrations, deactivate and delete U1 to U5.
- Help desk: untick the MFA roles, clear *Allowed email domains*, switch *Enable SAML login* and OIDC off, or
  discard the stack.
- Delete `sp.key` and `sp.crt`.

---

## List 5. Microsoft Entra ID

**Why:** Entra sends its claims under long Microsoft names and, by default, no sign of MFA. #619 part 4 and the
upgrade notes both say this has never been run against a real tenant. Entra is the SAML run; the OIDC part is
shorter and optional.

### Prerequisites

- An Entra tenant where you can create an Enterprise application (a free Microsoft 365 developer tenant is
  fine). Test users: **E1** (with a mailbox, so `mail` is filled in, a given name and a surname), **E2** whose
  email is **A's address**, and **E3** with no mailbox (only a user principal name) for the fallback step.
- Shared setup done. Certificate and key as in list 4 (reuse `sp.crt` / `sp.key`).
- SAML box and OIDC box as in list 4.

### Steps (SAML)

- [ ] **5.1** In Entra: Enterprise applications, New application, **Create your own application**, choose the
  "integrate any other application (non-gallery)" option. Open **Single sign-on**, **SAML**, edit **Basic SAML
  Configuration**: **Identifier (Entity ID)** `{BASE_URL}/api/v1/auth/saml/metadata`, **Reply URL (Assertion
  Consumer Service URL)** `{BASE_URL}/api/v1/auth/saml/acs`, **Sign on URL** `{BASE_URL}/api/v1/auth/saml/login`.
  Under **Users and groups** assign E1 and E2. Under **SAML Certificates** copy **App Federation Metadata
  Url**.
- [ ] **5.2** Help desk as A: as in 4.2, paste that URL into **IdP metadata URL**, upload the SP certificate
  and key, **Save SAML config**. **Expect:** "SAML config saved." and **Configured**. Open the SP metadata and
  check `entityID` and ACS as in 4.3.
- [ ] **5.3** Sign out, **Sign in with SAML** as **E1**. **Expect:** signed in, role **User**. Admin, Users,
  E1: the **display name is E1's given name and surname**, not the user principal name (the design ranks
  Entra's `name` claim last because it carries the UPN), and the **email** is the mailbox address.
  Record both exactly as shown.
- [ ] **5.4** Sign in again and again. **Expect:** same account each time.
- [ ] **5.5 No mailbox.** Assign **E3**, sign in with SAML as E3. **Expect:** record what happens. The design
  says the email falls back to the NameID; if that is a UPN that is not a valid address you should see "Your
  identity provider sent an email address this help desk cannot use". Either a signed-in account or that
  message is acceptable; a blank page or a 500 is a fail.
- [ ] **5.6 MFA is not asserted by default.** Give E1 an authenticator app in the help desk first: sign E1 in,
  **Your account**, **Set up an authenticator app**, finish setup. Sign out and in again through SAML.
  **Expect:** no code is asked at sign-in (SSO counts as MFA). On the **Account** page, **Add a passkey**.
  **Expect:** the **Verify your second factor** panel with a code field, because Entra did not say MFA was
  done. Cancel.
- [ ] **5.7 MFA asserted.** In Entra, on the app's **Single sign-on**, Attributes & Claims, add the optional
  `amr` claim with `include_granular_amr` as the design says, and make sure E1 signs in with MFA at Entra.
  Sign out and in. **Expect:** on **Add a passkey** no panel appears; the sheet comes straight up (cancel it).
  Mark `[N]` if your tenant cannot add the claim.

### Negative cases (SAML, Entra)

- [ ] **5.8 IdP-initiated is refused.** In Entra on the app, **Test**, or open the app from `myapps.microsoft.com`.
  **Expect:** not signed in; a 403 page.
- [ ] **5.9 Replay is refused.** As in 4.9 (cURL replay of `POST .../saml/acs` including its `saml_` cookie).
  **Expect:** `403`.
- [ ] **5.10 Hand-over replay is refused.** As in 4.10. **Expect:** `/login?error=sso_session_used`.
- [ ] **5.11 Administrator email is never linked.** Sign in with SAML as **E2**. **Expect:** "Another account
  here already uses the email address your identity provider sent..." and A unchanged.
- [ ] **5.12 Failsafe.** As in 4.13: with *Enable SAML login* saved on, a local non-admin cannot use a password
  and administrator A can.

### Optional: Entra by OIDC

- [ ] **5.13** App registration with Web platform and **Redirect URI** `{BASE_URL}/api/v1/auth/oidc/callback`,
  a client secret, and the optional `email` (and `amr`) claims. **Issuer URL** is
  `https://login.microsoftonline.com/<tenant-id>/v2.0`. Configure as in 4.16 and sign in as E1. **Expect:**
  signed in. **Record whether the sign-in was refused with "Your identity provider did not send a verified
  email address".** The code drops an email that does not come with `email_verified`; whether Entra's ID token
  includes that claim was not checked when this list was written, so a refusal is a real finding to report, not
  a mistake in your setup.

### Cleanup

- Entra: delete the Enterprise application and any app registration, delete E1 to E3.
- Help desk: clear *Enable SAML login*, OIDC off, or discard the stack. Delete `sp.key` and `sp.crt`.

---

## List 6. Google, by OIDC

**Why:** Google sends a verified email and no MFA claim; the allowed-domain rule applies to every OIDC sign-in,
not just the first; a Google Workspace account and a plain Gmail account differ only by domain.

### Prerequisites

- A Google Cloud project with an **OAuth consent screen** (External, in Testing mode with your test accounts
  added, or Internal for a Workspace org) and an **OAuth client ID** of type Web application with
  **Authorized redirect URI** `{BASE_URL}/api/v1/auth/oidc/callback`.
- Two Google accounts: **G1** (a Workspace account, domain `yourcompany.com`) and **G2** (a plain
  `@gmail.com` account). A third **G3** whose email equals a local **staff** account K's address (change K's
  email to G3's, or create a local staff account with it).
- SMTP settings for the throwaway stack, because the allowed-domain list only shows while *Allow self-service
  signup* is on and the server refuses that without email: set any values for `SMTP_HOST` and `SMTP_FROM` in
  `docker/.env` and restart (the page's own error says this). Mail does not have to work.
- Shared setup done.

### Steps

- [ ] **6.1** As A, OIDC box: **Issuer URL** `https://accounts.google.com`, **Client ID**, **Client secret**
  from Google, **Enable OIDC login** on, click **Save changes** (the OIDC one). **Expect:** "OIDC config
  saved.", **Configured**, **Redirect URL** equal to the one registered at Google.
- [ ] **6.2** Sign out. **Sign in with OIDC** as **G1**. **Expect:** Google's account chooser, then signed in
  as a new account with role **User**, name and email from Google. Admin, Users: **Login type** **SSO (OIDC)**.
- [ ] **6.3** Sign out and in again. **Expect:** same account, one row.
- [ ] **6.4 A local account is adopted.** Sign in with OIDC as **G3** (K's address). **Expect:** you are in as
  K's existing staff account, not a new one; **Login type** **Local + SSO**. Check that K's role is still
  **Staff**.
- [ ] **6.5 SSO counts as MFA.** Turn *Enable MFA* on and *Require MFA for roles* **User**, Save. Sign in as G1.
  **Expect:** straight in (no code, no enrolment). Untick the role afterwards.

### Domain rules

- [ ] **6.6 Set a domain list.** Switch *Allow self-service signup* on, in **Allowed email domains** type
  `yourcompany.com`, click away from the box and press the page's **Save changes**. Switch signup off again and
  save once more if you do not want signup left on. **Expect:** saved.
- [ ] **6.7 Wrong domain is refused every time.** Sign in with OIDC as **G2** (gmail.com). **Expect:** "Accounts
  from your email domain cannot sign in here." and no account is created. *Why:* the rule applies to every
  OIDC sign-in, including accounts that already exist.
- [ ] **6.8 Existing person at a removed domain.** Change the list to `other.example`, then sign in as **G1**
  (who signed in before). **Expect:** refused with the same message (unlike SAML, OIDC re-checks the domain
  each time). Put the list back to `yourcompany.com` and confirm G1 works again.
- [ ] **6.9 Non-ASCII domains.** In **Allowed email domains** type `bücher.de` and save. **Expect:** refused
  (`400`) with a message saying the entry must be ASCII and naming `xn--bcher-kva.de`. Type `xn--bcher-kva.de`
  instead and save. **Expect:** accepted. Then restore `yourcompany.com`. *Why:* an internationalised domain
  must be entered in its `xn--` form, and the page should tell you so. The sign-in side of this (an existing
  account at such a domain) has no real-provider test here, because Google cannot issue a sign-in at that
  domain; it is covered by the automated tests only. Say so in the release notes if you care.

### Negative cases

- [ ] **6.10 Not linked onto an administrator.** Create a Google account **G4** with A's address (or add A's
  address as a Google alias if your Workspace allows it). Sign in with OIDC. **Expect:** "Your single sign-on
  identity could not be linked to the existing account with that email address. Ask an administrator." Mark
  `[N]` if you cannot make a Google account with A's address.
- [ ] **6.11 Callback replay is refused.** As 4.22: paste a used callback URL after signing out. **Expect:**
  "Your sign-in expired or was started in another window."
- [ ] **6.12 Wrong secret.** As 4.23.
- [ ] **6.13 Last administrator.** As 4.24: switching OIDC off or clearing the issuer does not stop A signing in
  with the password.

### Optional: Google Workspace as a SAML provider

Run this only if you have a Workspace admin console. It covers the pasted-XML path: Google gives a metadata
download, not a URL, and the design says a pasted document cannot refresh by itself.

- [ ] **6.14** Workspace Admin console, Apps, Web and mobile apps, Add custom SAML app. Download the IdP
  metadata. Enter **ACS URL** `{BASE_URL}/api/v1/auth/saml/acs`, **Entity ID**
  `{BASE_URL}/api/v1/auth/saml/metadata`, **Name ID format** EMAIL, **Name ID** Basic Information, Primary
  email. Turn the app on for your test user.
- [ ] **6.15** Help desk, SAML box: choose **XML**, **Upload .xml** with the downloaded file (or paste it),
  add the SP certificate and key, **Save SAML config**. **Expect:** saved and **Configured**. Reopen: the XML
  is shown again (it is public data). Sign in with **Sign in with SAML**. **Expect:** signed in. Repeat the
  replay checks (4.9, 4.10) if time allows.

### Cleanup

- Google Cloud: delete the OAuth client and, if it was only for this, the project; remove test users from the
  consent screen. Workspace: delete the custom SAML app.
- Help desk: clear the allowed domains, OIDC off, remove the SMTP placeholder values, or discard the stack.

---

## Result sheet

| List | Who/what ran it | Pass | Fail | Not run | Notes (link to screenshots) |
|---|---|---|---|---|---|
| 1 Passkeys, Safari on macOS | | | | | |
| 2 Passkeys, iOS Safari, iCloud Keychain | | | | | |
| 3 Hardware key, sign count, admin removal | | | | | |
| 4 Okta, SAML and OIDC | | | | | |
| 5 Microsoft Entra ID | | | | | |
| 6 Google, OIDC (and Workspace SAML) | | | | | |

**Release-notes wording for anything not run:** "Not verified in a real environment: <list and step>." Name
the thing, not the list number, for example "Passkeys on iOS Safari", "A hardware security key's sign-count
check", "Sign-in against Microsoft Entra ID", "The hourly SAML metadata refresh and certificate rollover".

**Never covered by these lists (say so in the notes if still true at the tag):**
- The hourly refresh of IdP metadata and a certificate rollover: they need an IdP that rotates its certificate,
  or a wait of an hour with a changed URL.
- Docker Compose on a native arm64 host with buildx (#632 item 2): a separate manual run.
- Attestation and a "passkey required" policy: not built in 1.3.0 (documented limits, 1.3.x).

**Discard the throwaway stack** when finished, from its own copy of `docker/` only:
`docker compose -p rc130 down` and delete that copy's `data/postgres`. It holds test passkeys, SSO identities
and a fake administrator.
