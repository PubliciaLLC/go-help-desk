ALTER TABLE oauth_clients DROP COLUMN user_id;

-- ticket_status_history.changed_by_user_id has no ON DELETE action, and a
-- service user may by now have changed a ticket's status. Keep the history
-- row, drop the pointer to the user about to be deleted.
UPDATE ticket_status_history SET changed_by_user_id = NULL
WHERE changed_by_user_id IN (SELECT id FROM users WHERE service_account);

-- A ticket an OAuth client created with no explicit reporter has
-- reporter_user_id = its service user and guest_email NULL. Deleting the user
-- sets reporter_user_id NULL (ON DELETE SET NULL), which would break
-- ticket_reporter_or_guest and fail this whole rollback. 1.2.0 knows no
-- service users, so the ticket becomes a guest ticket: named after the service
-- user (a name already on the ticket is kept) and with an EMPTY address. Empty,
-- not NULL, because the check wants one of the two; and not a placeholder
-- address, because 1.2.0 mailed every staff reply to whatever was there, and
-- 1.3.0 queued guest notices to it with retries. Both skip an empty recipient.
UPDATE tickets t
SET guest_email = '',
    guest_name  = CASE WHEN t.guest_name = '' THEN u.display_name ELSE t.guest_name END
FROM users u
WHERE t.reporter_user_id = u.id AND u.service_account AND t.guest_email IS NULL;

DELETE FROM users WHERE service_account;

ALTER TABLE users DROP CONSTRAINT users_service_account_inert;
ALTER TABLE users DROP COLUMN service_account;
