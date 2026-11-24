-- Add guest name and phone to tickets for unauthenticated submissions.
ALTER TABLE tickets ADD COLUMN guest_name  TEXT NOT NULL DEFAULT '';
ALTER TABLE tickets ADD COLUMN guest_phone TEXT NOT NULL DEFAULT '';
