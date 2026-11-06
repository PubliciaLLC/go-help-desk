-- payload_format lets an outbound webhook subscription ask for its body
-- reshaped for a chat/ITSM service's incoming-webhook endpoint instead of the
-- full raw event. See docs/DESIGN.md "Notifications" and #187.
--
-- NOT NULL DEFAULT 'raw' means every existing row reads back as 'raw' with no
-- data migration and no change in behaviour: today's webhooks keep receiving
-- exactly what they receive now. The CHECK is the last place a bad value can
-- be refused; the admin handler refuses it first so the operator gets a 400,
-- not a constraint violation surfaced as a 500.
ALTER TABLE webhook_configs
    ADD COLUMN payload_format TEXT NOT NULL DEFAULT 'raw'
        CHECK (payload_format IN ('raw', 'slack', 'teams', 'discord', 'jira'));
