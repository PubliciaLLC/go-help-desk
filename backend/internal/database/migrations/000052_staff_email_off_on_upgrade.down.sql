-- Deliberately a no-op. The up step cannot be told apart afterwards from an
-- administrator who chose "off" (or inserted it on a beta database), so a
-- DELETE would also erase real choices. And removing the rows would flip an
-- upgraded install back to mailing every staff member, which is the one
-- outcome this migration exists to prevent. The rows are plain settings that
-- older code never reads.
SELECT 1;
