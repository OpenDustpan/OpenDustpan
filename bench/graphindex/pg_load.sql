\timing on
DROP TABLE IF EXISTS identity_produced, identity_consumed, units, graph_derived_edges, sets_whole, sets_chunk;
CREATE TABLE units (unit_seq bigint PRIMARY KEY, ns_seq int NOT NULL, size int NOT NULL, coll int NOT NULL);
CREATE TABLE identity_produced (ns_seq int NOT NULL, value_hash bigint NOT NULL, unit_seq bigint NOT NULL, class smallint NOT NULL, PRIMARY KEY (ns_seq, value_hash, unit_seq));
CREATE TABLE identity_consumed (ns_seq int NOT NULL, value_hash bigint NOT NULL, unit_seq bigint NOT NULL, PRIMARY KEY (ns_seq, value_hash, unit_seq));
\copy units FROM 'units.csv' CSV
\copy identity_produced FROM 'prod.csv' CSV
\copy identity_consumed FROM 'cons.csv' CSV
CREATE TABLE graph_derived_edges (source_seq bigint NOT NULL, dependent_seq bigint NOT NULL, evidence smallint NOT NULL, rank smallint NOT NULL, matches int NOT NULL, gen_source bigint NOT NULL, gen_dependent bigint NOT NULL, first_seen_ms bigint NOT NULL, last_seen_ms bigint NOT NULL, PRIMARY KEY (source_seq, dependent_seq, evidence));
INSERT INTO graph_derived_edges SELECT p.unit_seq, c.unit_seq, 5, min(p.class), count(*), 1, 1, 1730000000000, 1730000000000 FROM identity_consumed c JOIN identity_produced p ON p.ns_seq=c.ns_seq AND p.value_hash=c.value_hash WHERE p.unit_seq<>c.unit_seq GROUP BY p.unit_seq, c.unit_seq;
CREATE INDEX graph_derived_edges_dependent ON graph_derived_edges (dependent_seq, source_seq);
-- per-unit sets: whole array (one row per unit and side) vs chunks of at most 512 values by hash prefix
CREATE TABLE sets_whole (unit_seq bigint NOT NULL, side smallint NOT NULL, vals bytea NOT NULL, PRIMARY KEY (unit_seq, side));
INSERT INTO sets_whole SELECT unit_seq, 1, string_agg(int8send(value_hash), ''::bytea ORDER BY value_hash) FROM identity_produced GROUP BY unit_seq;
CREATE TABLE sets_chunk (unit_seq bigint NOT NULL, side smallint NOT NULL, bucket int NOT NULL, vals bytea NOT NULL, PRIMARY KEY (unit_seq, side, bucket));
INSERT INTO sets_chunk SELECT p.unit_seq, 1, ((p.value_hash >> 40) & ((1 << b.bits) - 1))::int, string_agg(int8send(p.value_hash), ''::bytea ORDER BY p.value_hash)
 FROM identity_produced p JOIN (SELECT unit_seq, greatest(0, ceil(log(2, greatest(count(*),1)/512.0)))::int AS bits FROM identity_produced GROUP BY unit_seq) b USING (unit_seq) GROUP BY 1,2,3;
VACUUM ANALYZE;
SELECT relname, n_live_tup, pg_total_relation_size(relid) AS bytes, round(pg_total_relation_size(relid)::numeric/greatest(n_live_tup,1),1) AS per_row FROM pg_stat_user_tables ORDER BY 1;
