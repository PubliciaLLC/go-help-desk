-- name: ListEmailTemplateOverrides :many
SELECT * FROM email_template_overrides ORDER BY name;

-- name: GetEmailTemplateOverrides :many
SELECT * FROM email_template_overrides WHERE name = ANY(@names::text[]) ORDER BY name;

-- Locks the row (if any) so the audit entry's "before" is the row this
-- transaction then replaces.
-- name: LockEmailTemplateOverride :one
SELECT * FROM email_template_overrides WHERE name = $1 FOR UPDATE;

-- name: UpsertEmailTemplateOverride :exec
INSERT INTO email_template_overrides (name, subject, body, updated_at, updated_by)
VALUES ($1, $2, $3, now(), $4)
ON CONFLICT (name) DO UPDATE
SET subject = EXCLUDED.subject, body = EXCLUDED.body, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: DeleteEmailTemplateOverride :exec
DELETE FROM email_template_overrides WHERE name = $1;
