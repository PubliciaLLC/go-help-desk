-- Drops the counters, which resets every limit: anyone who was over a budget
-- when this runs has a full one afterwards. The limits themselves are in code
-- and are not affected.
DROP TABLE rate_limit_counters;
