-- Migration 000072: the person's own appearance choice (#709, 1.3.0): light,
-- dark, or device (follow the browser's prefers-color-scheme). See docs/DESIGN.md,
-- Appearance (dark mode).
--
-- 000072 is a placeholder number. 000070 (CC recipients) and 000071 (branding
-- icon) are being built in parallel; whichever branch merges later renumbers.
-- Nothing here depends on the number.
--
-- NOT NULL DEFAULT 'device': "I never chose" has one spelling, and every account
-- that existed before the column reads as device, which is what 1.2 looked like.
-- The default is kept (it is not history to be marked): it is the value of every
-- account that never chose, and CreateUser does not name the column.
--
-- The CHECK is the backstop for the service's validation: the three values and
-- nothing else. No table rewrite: a constant default is a catalogue change only.
ALTER TABLE users ADD COLUMN theme text NOT NULL DEFAULT 'device'
    CONSTRAINT users_theme_valid CHECK (theme IN ('light', 'dark', 'device'));
