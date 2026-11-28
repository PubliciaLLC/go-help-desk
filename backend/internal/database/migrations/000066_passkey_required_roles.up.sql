-- Roles whose members must finish every sign-in with a passkey (#616c).
--
-- Empty on upgrade, so every login path behaves exactly as before until an
-- administrator ticks a role. It is its own list: nothing writes
-- mfa_enforced_roles on its behalf, because a role that must use a passkey is
-- treated as MFA-enforced when the settings are read (admin.MFARequiredFor).
INSERT INTO settings (key, value) VALUES ('passkey_required_roles', '[]') ON CONFLICT DO NOTHING;
