-- Benchmark seed for the audit scope statements (#331, #353).
-- 30,000 tickets, 524,000 audit rows, five staff members:
--   bench-all@   sees every ticket (a group whose scope covers all 10 categories)
--   bench-10@    sees ~10%          (a group scoped to one category, typeless rule)
--   bench-5@     sees 5 tickets     (reporter of 5 tickets, in no group)
--   bench-type@  sees ~7%           (one rule with a type)
--   bench-group@ sees ~2.5%         (member of a group with no rule; tickets assigned to it)
-- Actions: 'updated' ~60%, 'rare_action' ~500 rows, others spread.
-- Times: entries follow their ticket in time; the newest ~600 rows are within
-- the last 10 minutes. Deterministic (setseed) so runs are comparable.
--
-- USAGE: Run against a MIGRATED, EMPTY throwaway database only. Never against
-- real data. Example with a private Docker Postgres container:
--
--   docker run -d --rm --name ghd-bench-331 -e POSTGRES_DB=helpdesk_test \
--     -e POSTGRES_USER=helpdesk -e POSTGRES_PASSWORD=helpdesk \
--     -p 127.0.0.1::5432 --tmpfs /var/lib/postgresql/data postgres:17-alpine
--   PORT=$(docker port ghd-bench-331 5432 | cut -d: -f2)
--   # Run migrations first (any one DB test)
--   cd backend && TEST_DATABASE_URL="postgres://helpdesk:helpdesk@127.0.0.1:$PORT/helpdesk_test?sslmode=disable" \
--     go test ./internal/database/ -run TestAuditStore_Search_ScopedTo -count=1
--   # Seed the database
--   docker exec -i ghd-bench-331 psql -q -v ON_ERROR_STOP=1 -U helpdesk helpdesk_test < scripts/bench/audit-scope-seed.sql
--   # Measure: EXPLAIN (ANALYZE, BUFFERS) EXECUTE on the audit statements for the
--   # five bench-* users, comparing buffers, not times under load. See #331.
--
BEGIN;
SELECT setseed(0.331);

INSERT INTO users (id, email, display_name, role)
SELECT gen_random_uuid(), 'u' || g || '@bench.invalid', 'User ' || g, 'user'
FROM generate_series(1, 1000) g;
INSERT INTO users (email, display_name, role) VALUES
  ('bench-all@bench.invalid', 'Bench All', 'staff'),
  ('bench-10@bench.invalid',  'Bench Ten', 'staff'),
  ('bench-5@bench.invalid',   'Bench Five', 'staff');

INSERT INTO categories (name, sort_order) SELECT 'Cat ' || g, g FROM generate_series(0, 9) g;
INSERT INTO types (category_id, name)
SELECT c.id, 'Type ' || c.name || '/' || g FROM categories c, generate_series(1, 2) g;

INSERT INTO groups (name) SELECT 'Group ' || g FROM generate_series(1, 20) g;
INSERT INTO groups (name) VALUES ('Bench All Group'), ('Bench Ten Group');
INSERT INTO group_members (group_id, user_id)
SELECT g.id, u.id FROM groups g, users u WHERE g.name = 'Bench All Group' AND u.email = 'bench-all@bench.invalid';
INSERT INTO group_members (group_id, user_id)
SELECT g.id, u.id FROM groups g, users u WHERE g.name = 'Bench Ten Group' AND u.email = 'bench-10@bench.invalid';
INSERT INTO group_scopes (group_id, category_id)
SELECT g.id, c.id FROM groups g, categories c WHERE g.name = 'Bench All Group';
INSERT INTO group_scopes (group_id, category_id)
SELECT g.id, c.id FROM groups g, categories c WHERE g.name = 'Bench Ten Group' AND c.name = 'Cat 0';
-- Noise: the other groups cover a category each, and have members.
INSERT INTO group_scopes (group_id, category_id)
SELECT g.id, (SELECT id FROM categories ORDER BY sort_order OFFSET (substr(g.name, 7)::int % 10) LIMIT 1)
FROM groups g WHERE g.name LIKE 'Group %';
INSERT INTO group_members (group_id, user_id)
SELECT g.id, u.id FROM groups g JOIN users u ON u.email LIKE 'u%@bench.invalid'
WHERE g.name LIKE 'Group %' AND random() < 0.01;

CREATE TEMP TABLE cat_ix AS SELECT id, sort_order AS ix FROM categories;
CREATE TEMP TABLE type_ix AS
  SELECT t.id, t.category_id, row_number() OVER (PARTITION BY t.category_id ORDER BY t.name) AS n FROM types t;
CREATE TEMP TABLE user_ix AS
  SELECT id, row_number() OVER (ORDER BY email) AS n FROM users WHERE email LIKE 'u%@bench.invalid';
CREATE TEMP TABLE group_ix AS
  SELECT id, row_number() OVER (ORDER BY name) AS n FROM groups WHERE name LIKE 'Group %';

-- Tickets: n = 1..30000, created oldest first, about 6 days in all.
CREATE TEMP TABLE tk AS
SELECT g AS n,
       gen_random_uuid() AS id,
       (g % 10) AS cat,
       (CASE WHEN g % 3 = 0 THEN NULL ELSE 1 + (g % 2) END) AS typ,
       1 + floor(random() * 1000)::int AS reporter,
       CASE WHEN random() < 0.5 THEN 1 + floor(random() * 1000)::int END AS assignee,
       CASE WHEN random() < 0.5 THEN 1 + floor(random() * 20)::int END AS agroup,
       now() - interval '6 days' + (g * (interval '6 days' - interval '5 minutes') / 30000) AS created_at
FROM generate_series(1, 30000) g;

-- Descriptions are ~600 characters of distinct tokens so tickets rows (and
-- their generated search_vector) are about as wide as real ones: the cost of
-- building a visible-ticket set is a scan of this table, so its width matters.
INSERT INTO tickets (id, tracking_number, subject, description, category_id, type_id, status_id,
                     assignee_user_id, assignee_group_id, reporter_user_id, created_at, updated_at)
SELECT tk.id, 'BENCH-' || tk.n, 'Ticket ' || tk.n,
       (SELECT string_agg(md5(random()::text || g || x.n), ' ') FROM generate_series(1, 18) g, (SELECT tk.n) x),
       c.id, ty.id,
       (SELECT id FROM statuses WHERE name = 'New'),
       ua.id, gi.id, ur.id, tk.created_at, tk.created_at
FROM tk
JOIN cat_ix c ON c.ix = tk.cat
LEFT JOIN type_ix ty ON ty.category_id = c.id AND ty.n = tk.typ
JOIN user_ix ur ON ur.n = tk.reporter
LEFT JOIN user_ix ua ON ua.n = tk.assignee
LEFT JOIN group_ix gi ON gi.n = tk.agroup;

-- bench-5 reported 5 tickets spread through history.
UPDATE tickets SET reporter_user_id = (SELECT id FROM users WHERE email = 'bench-5@bench.invalid')
WHERE tracking_number IN ('BENCH-1000', 'BENCH-8000', 'BENCH-15000', 'BENCH-22000', 'BENCH-29000');

-- Audit: 17 entries per ticket (510,000), each within 10 minutes after the
-- ticket, plus 14,000 non-ticket entries. The newest ~600 fall in the last 10
-- minutes because the whole set spans ~6 days ending about now.
-- before/after carry a small object, as real entries do, so audit rows are
-- about as wide as real ones too.
INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, before, after, created_at)
SELECT gen_random_uuid(),
       ur.id,
       'ticket',
       tk.id,
       CASE WHEN k = 1 THEN 'created'
            WHEN r < 0.001 THEN 'rare_action'
            WHEN r < 0.64 THEN 'updated'
            WHEN r < 0.80 THEN 'status_changed'
            WHEN r < 0.92 THEN 'assigned'
            ELSE 'resolved' END,
       jsonb_build_object('status_id', gen_random_uuid()),
       jsonb_build_object('status_id', gen_random_uuid()),
       tk.created_at + random() * interval '10 minutes'
FROM (SELECT tk.*, k, random() AS r FROM tk CROSS JOIN generate_series(1, 17) k) tk
JOIN user_ix ur ON ur.n = tk.reporter;

INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, created_at)
SELECT gen_random_uuid(), NULL, 'user', gen_random_uuid(),
       CASE WHEN random() < 0.6 THEN 'updated' ELSE 'login' END,
       now() - interval '6 days' + (g * interval '6 days' / 14000)
FROM generate_series(1, 14000) g;

-- Two more shapes of staff: one type rule (~7% of tickets), and group
-- assignment only (a group with no scope rule, ~2.5% of tickets).
INSERT INTO users (email, display_name, role) VALUES
  ('bench-type@bench.invalid',  'Bench Type', 'staff'),
  ('bench-group@bench.invalid', 'Bench Group', 'staff');
INSERT INTO groups (name) VALUES ('Bench Type Group');
INSERT INTO group_members (group_id, user_id)
SELECT g.id, u.id FROM groups g, users u WHERE g.name = 'Bench Type Group' AND u.email = 'bench-type@bench.invalid';
INSERT INTO group_scopes (group_id, category_id, type_id)
SELECT g.id, ty.category_id, ty.id FROM groups g, types ty JOIN categories c ON c.id = ty.category_id
WHERE g.name = 'Bench Type Group' AND c.name = 'Cat 1' AND ty.name = 'Type Cat 1/2';
-- 'Group 20' has no scope rule of its own after this; tickets assigned to it are the way in.
DELETE FROM group_scopes WHERE group_id = (SELECT id FROM groups WHERE name = 'Group 20');
INSERT INTO group_members (group_id, user_id)
SELECT g.id, u.id FROM groups g, users u WHERE g.name = 'Group 20' AND u.email = 'bench-group@bench.invalid';

COMMIT;
VACUUM ANALYZE;
