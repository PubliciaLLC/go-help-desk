-- Indexes for the ticket list's filters and sorts (advanced search).
--
-- Indexes only: no column or row changes, so there is nothing for the down
-- migration to lose.
--
-- updated_at: the updated range and "sort by updated" read it, and until now
-- nothing did.
CREATE INDEX IF NOT EXISTS tickets_updated_at_idx ON tickets (updated_at DESC);

-- The requester free-text filter is a substring match (ILIKE '%text%') on the
-- ticket's guest name and guest email. A btree cannot serve a leading wildcard,
-- so these are trigram indexes, the way 000021 did it for tracking_number.
-- pg_trgm has been installed since 000021.
CREATE INDEX IF NOT EXISTS tickets_guest_name_trgm_idx  ON tickets USING gin (guest_name  gin_trgm_ops);
CREATE INDEX IF NOT EXISTS tickets_guest_email_trgm_idx ON tickets USING gin (guest_email gin_trgm_ops);
