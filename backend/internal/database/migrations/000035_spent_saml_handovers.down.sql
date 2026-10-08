-- Older code never reads this table. After rollback, a spent hand-over cookie
-- can be replayed until it expires, as before #337.
DROP TABLE spent_saml_handovers;
