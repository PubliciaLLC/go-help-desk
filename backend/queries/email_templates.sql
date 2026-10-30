-- name: ListEmailTemplateOverrides :many
SELECT * FROM email_template_overrides ORDER BY name;

-- name: GetEmailTemplateOverrides :many
SELECT * FROM email_template_overrides WHERE name = ANY(@names::text[]) ORDER BY name;

-- Held until the transaction ends. FOR UPDATE below locks a row only when there
-- is one, so two first-time saves of one template would both read "no override"
-- and the second would overwrite the first (and record the shipped text as what
-- it replaced). Locking the name serialises them whether or not a row exists.
-- name: LockEmailTemplateName :exec
SELECT pg_advisory_xact_lock(hashtextextended('email_template_override:' || @name::text, 0));

-- Locks the row (if any) so the audit entry's "before" is the row this
-- transaction then replaces. Taken after LockEmailTemplateName.
-- name: LockEmailTemplateOverride :one
SELECT * FROM email_template_overrides WHERE name = $1 FOR UPDATE;

-- html is NULL for a template with no HTML part.
-- name: UpsertEmailTemplateOverride :exec
INSERT INTO email_template_overrides (name, subject, body, html, updated_at, updated_by)
VALUES ($1, $2, $3, $4, now(), $5)
ON CONFLICT (name) DO UPDATE
SET subject = EXCLUDED.subject, body = EXCLUDED.body, html = EXCLUDED.html, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: DeleteEmailTemplateOverride :exec
DELETE FROM email_template_overrides WHERE name = $1;
