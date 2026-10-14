ALTER TABLE oauth_clients DROP COLUMN user_id;

-- ticket_status_history.changed_by_user_id has no ON DELETE action, and a
-- service user may by now have changed a ticket's status. Keep the history
-- row, drop the pointer to the user about to be deleted.
UPDATE ticket_status_history SET changed_by_user_id = NULL
WHERE changed_by_user_id IN (SELECT id FROM users WHERE service_account);

DELETE FROM users WHERE service_account;

ALTER TABLE users DROP CONSTRAINT users_service_account_inert;
ALTER TABLE users DROP COLUMN service_account;
