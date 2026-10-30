-- Dropping this table removes every registered passkey on the instance.
--
-- That is survivable while passkeys are a second factor — the password still
-- works and TOTP is untouched — and it is NOT survivable once passwordless
-- sign-in exists, where it would lock out every account that had stopped
-- using a password. Whoever adds passwordless has to revisit this file as
-- well as the last-admin guard; see the note at the three ...UnlessLastAdmin
-- statements in queries/users.sql.
DROP TABLE IF EXISTS webauthn_credentials;
