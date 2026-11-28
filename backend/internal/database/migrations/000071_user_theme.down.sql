-- Reverses 000071. Each person's appearance choice is lost; the web UI falls back
-- to following the device. IF EXISTS so a repeat is harmless.
ALTER TABLE users DROP COLUMN IF EXISTS theme;
