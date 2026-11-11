-- Seed for the ticket-list scale measurements (PR #651). Run against a migrated, EMPTY
-- database (about 9 minutes; the table is about 1.5 GB, well past shared_buffers):
--   GHD_TEST_INSTANCE=bench ./scripts/test-db.sh up
--   docker exec -i ghd-test-bench-testdb-1 psql -U helpdesk -d helpdesk_test -v ON_ERROR_STOP=1 < scripts/bench-ticket-list/seed.sql
-- The database is migrated by running any database test once (testutil.NewDB migrates it).
-- Then see backend/internal/database/ticketlist_scale_test.go and parse_plans.py.
-- 1,000,000 tickets, 20,000 requesters, 10 categories x 3 types, 3 groups.
-- Fixed ids: admin ...a1, staff in group G1 ...b1, staff in no group ...b2, requester R ...c1.
SELECT setseed(0.42);
INSERT INTO users (id, email, display_name, role) VALUES
 ('00000000-0000-0000-0000-0000000000a1','admin@x.test','Admin','admin'),
 ('00000000-0000-0000-0000-0000000000b1','staffg@x.test','Staff G1','staff'),
 ('00000000-0000-0000-0000-0000000000b2','staffn@x.test','Staff NoGroup','staff'),
 ('00000000-0000-0000-0000-0000000000d1','s1@x.test','Staff 1','staff'),
 ('00000000-0000-0000-0000-0000000000d2','s2@x.test','Staff 2','staff'),
 ('00000000-0000-0000-0000-0000000000d3','s3@x.test','Staff 3','staff'),
 ('00000000-0000-0000-0000-0000000000d4','s4@x.test','Staff 4','staff'),
 ('00000000-0000-0000-0000-0000000000c1','r0@x.test','Requester 0','user');
INSERT INTO users (id, email, display_name, role)
SELECT md5('req'||i)::uuid, 'r'||i||'@x.test', 'Requester '||i, 'user' FROM generate_series(1,19999) i;
INSERT INTO categories (id, name, sort_order) SELECT md5('cat'||i)::uuid, 'Cat '||i, i FROM generate_series(0,9) i;
INSERT INTO types (id, category_id, name, sort_order) SELECT md5('type'||c||'-'||t)::uuid, md5('cat'||c)::uuid, 'Type '||c||'-'||t, t FROM generate_series(0,9) c, generate_series(0,2) t;
INSERT INTO groups (id, name) VALUES ('00000000-0000-0000-0000-0000000000f1','G1'),('00000000-0000-0000-0000-0000000000f2','G2'),('00000000-0000-0000-0000-0000000000f3','G3');
INSERT INTO group_members (group_id, user_id) VALUES
 ('00000000-0000-0000-0000-0000000000f1','00000000-0000-0000-0000-0000000000b1'),
 ('00000000-0000-0000-0000-0000000000f2','00000000-0000-0000-0000-0000000000d1'),
 ('00000000-0000-0000-0000-0000000000f3','00000000-0000-0000-0000-0000000000d2');
-- G1 covers category 0 whole (about 10% of tickets) and one type of category 1 (about 3%).
INSERT INTO group_scopes (group_id, category_id, type_id) VALUES
 ('00000000-0000-0000-0000-0000000000f1', md5('cat0')::uuid, NULL),
 ('00000000-0000-0000-0000-0000000000f1', md5('cat1')::uuid, md5('type1-0')::uuid);
INSERT INTO group_scopes (group_id, category_id, type_id) VALUES
 ('00000000-0000-0000-0000-0000000000f2', md5('cat2')::uuid, NULL),
 ('00000000-0000-0000-0000-0000000000f3', md5('cat3')::uuid, NULL);

-- 70% Closed, the rest spread over the other statuses; created over four years.
INSERT INTO tickets (tracking_number, subject, description, category_id, type_id, priority, status_id,
                     assignee_user_id, assignee_group_id, reporter_user_id, created_at, updated_at,
                     description_format, source_channel)
SELECT 'GHD-2023-' || lpad(g::text, 8, '0'),
       'Ticket ' || g || ' printer ' || (g % 97),
       repeat(md5(g::text), 19),
       md5('cat' || (g % 10))::uuid,
       CASE WHEN g % 3 = 0 THEN md5('type' || (g % 10) || '-' || (g % 3))::uuid END,
       (ARRAY['low','medium','medium','high','critical'])[1 + g % 5],
       (SELECT id FROM statuses WHERE name = CASE WHEN g % 10 < 7 THEN 'Closed' WHEN g % 10 = 7 THEN 'New' WHEN g % 10 = 8 THEN 'In Progress' ELSE 'Pending' END),
       CASE WHEN g % 4 = 0 THEN ('00000000-0000-0000-0000-0000000000d' || (1 + g % 4)::text)::uuid END,
       CASE WHEN g % 5 = 1 THEN ('00000000-0000-0000-0000-0000000000f' || (1 + (g/5) % 3)::text)::uuid END,
       md5('req' || (1 + g % 19999))::uuid,
       now() - ((1000000 - g) * interval '2 minutes') - interval '1 day',
       now() - ((1000000 - g) * interval '2 minutes') - interval '1 day',
       'plain', 'web'
FROM generate_series(1, 1000000) g;
-- requester R (c1): replace the reporter of every 20000th ticket, so 50 tickets spread over the whole range.
UPDATE tickets SET reporter_user_id = '00000000-0000-0000-0000-0000000000c1' WHERE tracking_number LIKE 'GHD-2023-%' AND substr(tracking_number, 10)::int % 20000 = 0;
-- Clear any assignment the generator gave the two measured staff, then give each 10 of the OLDEST tickets.
UPDATE tickets SET assignee_user_id = NULL WHERE assignee_user_id IN ('00000000-0000-0000-0000-0000000000b1','00000000-0000-0000-0000-0000000000b2');
UPDATE tickets SET assignee_user_id = '00000000-0000-0000-0000-0000000000b2' WHERE id IN (SELECT id FROM tickets ORDER BY created_at ASC LIMIT 10);
UPDATE tickets SET assignee_user_id = '00000000-0000-0000-0000-0000000000b1' WHERE id IN (SELECT id FROM tickets WHERE assignee_user_id IS NULL ORDER BY created_at ASC LIMIT 10);
VACUUM (ANALYZE) tickets;
VACUUM (ANALYZE) users;

-- Narrow visibility, the cases that must not grow with the table: a group that covers a rare
-- category (100 tickets) and one that covers a rare type of a large category (100 tickets).
INSERT INTO categories (id,name,sort_order) VALUES (md5('catrare')::uuid,'Rare',99);
INSERT INTO users (id,email,display_name,role) VALUES
 ('00000000-0000-0000-0000-0000000000b3','s_rare@x.test','Staff Rare Cat','staff'),
 ('00000000-0000-0000-0000-0000000000b4','s_raretype@x.test','Staff Rare Type','staff');
INSERT INTO groups (id,name) VALUES ('00000000-0000-0000-0000-0000000000f4','G4'),('00000000-0000-0000-0000-0000000000f5','G5');
INSERT INTO group_members VALUES ('00000000-0000-0000-0000-0000000000f4','00000000-0000-0000-0000-0000000000b3'),('00000000-0000-0000-0000-0000000000f5','00000000-0000-0000-0000-0000000000b4');
INSERT INTO group_scopes (group_id,category_id,type_id) VALUES ('00000000-0000-0000-0000-0000000000f4',md5('catrare')::uuid,NULL),('00000000-0000-0000-0000-0000000000f5',md5('cat8')::uuid,md5('type8-1')::uuid);
UPDATE tickets SET category_id = md5('catrare')::uuid, type_id = NULL WHERE id IN (SELECT id FROM tickets ORDER BY created_at ASC OFFSET 20 LIMIT 100);
UPDATE tickets SET type_id = md5('type8-1')::uuid WHERE id IN (SELECT id FROM tickets WHERE category_id = md5('cat8')::uuid ORDER BY created_at ASC LIMIT 100);
VACUUM (ANALYZE) tickets;
