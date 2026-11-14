\timing on
DROP TABLE IF EXISTS prod_text, prod_hash, cons_hash, cons_text, unit_sets, edges;
-- 3334 units x 300 resources = 1,000,200 resources; 2 produced identity values per resource.
-- unit 1..34 are hub units (network). ns: 10 namespaces.
CREATE TABLE prod_hash (ns int4 NOT NULL, h int8 NOT NULL, u int8 NOT NULL, strength int2 NOT NULL, PRIMARY KEY (ns,h,u));
INSERT INTO prod_hash SELECT (u % 10), (hashtextextended(u||':'||r||':'||k,0)), u, k::int2
 FROM generate_series(1,3334) u, generate_series(1,300) r, generate_series(1,2) k;
-- consumed: 1.5 distinct values per resource = 450 per unit: 315 from the hub of its ns (unit u%34+1 style), 135 from random units same ns
CREATE TABLE cons_hash (ns int4 NOT NULL, h int8 NOT NULL, u int8 NOT NULL, PRIMARY KEY (ns,h,u));
INSERT INTO cons_hash SELECT DISTINCT (c.u % 10), p.h, c.u FROM generate_series(35,3334) AS c(u)
 CROSS JOIN LATERAL (SELECT ((c.u % 34) + 1) AS hub) hh
 CROSS JOIN LATERAL (SELECT h FROM prod_hash WHERE u = hh.hub LIMIT 315) p
 ;
INSERT INTO cons_hash SELECT DISTINCT (c.u%10), hashtextextended(((c.u*7919+i) % 3334 + 1)||':'||(i%300+1)||':1',0), c.u
 FROM generate_series(35,3334) AS c(u), generate_series(1,135) AS g(i) ON CONFLICT DO NOTHING;
-- naive: text values (avg ARN ~70 chars, id ~24): store 'arn:aws:ec2:eu-west-1:123456789012:subnet/subnet-' || md5 (~ 70-90 bytes)
CREATE TABLE prod_text (namespace_id text NOT NULL, value text NOT NULL, tool text NOT NULL, unit_id text NOT NULL, PRIMARY KEY (namespace_id, value, tool, unit_id));
INSERT INTO prod_text SELECT 'ns_01JABCDEF0123456789ABCD'||(u%10), 'arn:aws:ec2:eu-west-1:123456789012:subnet/subnet-'||substr(md5(u||':'||r||':'||k),1,17), 'opentofu', 'ws-01JABCDEF0123456789'||lpad(u::text,5,'0')
 FROM generate_series(1,3334) u, generate_series(1,300) r, generate_series(1,2) k;
CREATE INDEX ON prod_hash (u);
-- per-unit packed sets for diffing instead of a (u,h) index
CREATE TABLE unit_sets (u int8 PRIMARY KEY, produced bytea, consumed bytea);
INSERT INTO unit_sets SELECT u, decode(repeat('00',600*8),'hex'), decode(repeat('00',450*8),'hex') FROM generate_series(1,3334) u;
-- GIN alternative
DROP TABLE IF EXISTS cons_arr; CREATE TABLE cons_arr (u int8 PRIMARY KEY, ns int4, hs int8[]);
INSERT INTO cons_arr SELECT u, min(ns), array_agg(h) FROM cons_hash GROUP BY u;
CREATE INDEX cons_arr_gin ON cons_arr USING gin (hs);
VACUUM ANALYZE;
SELECT relname, n_live_tup FROM pg_stat_user_tables ORDER BY 1;
SELECT c.relname, pg_size_pretty(pg_relation_size(c.oid)) heap, pg_size_pretty(pg_indexes_size(c.oid)) idx, pg_size_pretty(pg_total_relation_size(c.oid)) total, pg_total_relation_size(c.oid) bytes
 FROM pg_class c WHERE c.relname IN ('prod_hash','cons_hash','prod_text','unit_sets','cons_arr') ORDER BY 1;
SELECT indexrelid::regclass, pg_size_pretty(pg_relation_size(indexrelid)) FROM pg_index WHERE indrelid::regclass::text IN ('prod_hash','cons_hash','prod_text','cons_arr');
