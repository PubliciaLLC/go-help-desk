-- Durable TOTP attempt tracking.
--
-- The in-memory limiter that preceded this was cleared by any restart and was
-- kept per process, so N replicas multiplied every budget by N. A six-digit
-- code has about 3e-6 probability per guess and pquerna/otp accepts a ±1
-- window, so three codes are live at once: a patient attacker who already
-- holds the password works through a meaningful share of the space over weeks
-- if the counter keeps resetting.
--
-- NIST SP 800-63B and RFC 4226 section 7.3 both specify throttling per
-- account, and a per-account count is only meaningful if it survives a
-- restart.
ALTER TABLE users
    ADD COLUMN mfa_failed_attempts INT NOT NULL DEFAULT 0,
    -- When the account may next attempt a code. NULL means no lock.
    ADD COLUMN mfa_locked_until TIMESTAMPTZ;
