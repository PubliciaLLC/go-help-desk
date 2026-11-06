-- Per-user switches for staff email about tickets (#441). Each is on by
-- default, and NOT NULL DEFAULT TRUE turns them on for every existing row too:
-- that is the decision (an upgraded install starts mailing staff once email is
-- configured). An administrator can force each one on or off for everyone with
-- the staff_email_*_override settings, which need no migration.
--
--   email_notify_assigned  a ticket is assigned to me
--   email_notify_activity  a requester replies on a ticket assigned to me
--   email_notify_group     a ticket assigned to a group I belong to
ALTER TABLE users
    ADD COLUMN email_notify_assigned BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN email_notify_activity BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN email_notify_group    BOOLEAN NOT NULL DEFAULT TRUE;
