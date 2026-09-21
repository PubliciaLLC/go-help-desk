ALTER TABLE ticket_replies ADD COLUMN guest_token TEXT;
DROP TABLE guest_access_tokens;
