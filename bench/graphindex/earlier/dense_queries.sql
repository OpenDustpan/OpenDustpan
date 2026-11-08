\timing on
DROP TABLE IF EXISTS edges, scope;
CREATE TABLE edges (ns int4 NOT NULL, src int8 NOT NULL, dep int8 NOT NULL, kind int2 NOT NULL, evidence int2 NOT NULL, matches int4 NOT NULL, last_seen_ms int8 NOT NULL, PRIMARY KEY (src, dep, kind, evidence));
INSERT INTO edges SELECT min(c.ns), p.u, c.u, 2, 5, count(*), 1730000000000 FROM cons_hash c JOIN prod_hash p ON p.ns=c.ns AND p.h=c.h WHERE p.u<>c.u GROUP BY p.u, c.u;
-- the same prod/cons join ignoring ns (hubs are not aligned to ns in this synthetic data)
INSERT INTO edges SELECT 0, p.u, c.u, 2, 6, count(*), 1730000000000 FROM cons_hash c JOIN prod_hash p ON p.h=c.h WHERE p.u<>c.u GROUP BY p.u, c.u ON CONFLICT DO NOTHING;
CREATE INDEX edges_dep ON edges (dep, src);
-- grant scope of a caller: a units table with collection, caller reads 6 of 10 namespaces
CREATE TABLE units (u int8 PRIMARY KEY, ns int4 NOT NULL, coll int4 NOT NULL);
INSERT INTO units SELECT u, u%10, u%50 FROM generate_series(1,3334) u;
CREATE TABLE scope (ns int4 NOT NULL, coll int4); INSERT INTO scope SELECT g, NULL FROM generate_series(0,5) g; INSERT INTO scope VALUES (7, 7);
VACUUM ANALYZE;
SELECT count(*) edges, pg_size_pretty(pg_total_relation_size('edges')) FROM edges;
SELECT src, count(*) FROM edges GROUP BY src ORDER BY 2 DESC LIMIT 3;
-- Q1: plan blast probe: 1000 hashes of deleted resources of hub unit 1 -> consumer units
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT c.u, count(*) FROM cons_hash c WHERE c.ns = 1 AND c.h = ANY (ARRAY(SELECT h FROM prod_hash WHERE u=1 LIMIT 1000)) GROUP BY c.u;
-- Q2: one downstream hop for a frontier of 500 units, with the scope filter in the query
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF)
SELECT e.src, e.dep, e.kind, e.evidence, e.matches FROM edges e JOIN units d ON d.u = e.dep
WHERE e.src = ANY (ARRAY(SELECT generate_series(1,500)::int8)) AND e.last_seen_ms >= 0
  AND EXISTS (SELECT 1 FROM scope s WHERE s.ns = d.ns AND (s.coll IS NULL OR s.coll = d.coll))
ORDER BY e.last_seen_ms DESC, e.src, e.dep LIMIT 5001;
-- Q3: recursive CTE, 3 hops downstream from unit 40, scope filtered, cycles stopped
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF)
WITH RECURSIVE walk(u, hop, path) AS (
  SELECT 40::int8, 0, ARRAY[40::int8]
  UNION ALL
  SELECT e.dep, w.hop+1, w.path || e.dep FROM walk w JOIN edges e ON e.src = w.u JOIN units d ON d.u = e.dep
  WHERE w.hop < 3 AND NOT e.dep = ANY (w.path)
    AND EXISTS (SELECT 1 FROM scope s WHERE s.ns = d.ns AND (s.coll IS NULL OR s.coll = d.coll))
) SELECT u, min(hop) FROM walk GROUP BY u LIMIT 501;
