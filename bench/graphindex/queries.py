# Writes queries.json: the same four queries for PostgreSQL and SQLite, with
# the same literal parameters. Run it in the data directory after gen.py.
import json

fr = json.dumps(list(range(1, 501)))
sns = json.dumps([0, 1, 2, 3, 4, 5])
sco = json.dumps([7])
hs = [l.split(',')[1] for l in open('prod.csv') if l.split(',')[2] == '1'][:1000]
hsj = json.dumps([int(x) for x in hs])


def pg_any(p, t):
    return f"ANY(ARRAY(SELECT jsonb_array_elements_text('{p}'::jsonb)::{t}))"


def sq_list(p):
    return f"(SELECT value FROM json_each('{p}'))"


scope_pg = f"(d.ns_seq = {pg_any(sns, 'int')} OR d.coll = {pg_any(sco, 'int')})"
scope_sq = f"(d.ns_seq IN {sq_list(sns)} OR d.coll IN {sq_list(sco)})"


def q(pg):
    if pg:
        def a(p, t):
            return "= " + pg_any(p, t)
    else:
        def a(p, t):
            return "IN " + sq_list(p)
    sc = scope_pg if pg else scope_sq
    return {
        "probe": f"SELECT c.unit_seq, count(*) FROM identity_consumed c JOIN units d ON d.unit_seq=c.unit_seq WHERE c.ns_seq {a('[0]', 'int')} AND c.value_hash {a(hsj, 'bigint')} AND {sc} GROUP BY c.unit_seq",
        "down": f"SELECT e.source_seq, e.dependent_seq, e.rank, e.matches FROM graph_derived_edges e JOIN units d ON d.unit_seq=e.dependent_seq WHERE e.source_seq {a(fr, 'bigint')} AND e.matches > 0 AND {sc} ORDER BY e.rank, e.last_seen_ms DESC, e.source_seq, e.dependent_seq LIMIT 5001",
        "up": f"SELECT e.source_seq, e.dependent_seq, e.rank, e.matches FROM graph_derived_edges e JOIN units d ON d.unit_seq=e.source_seq WHERE e.dependent_seq {a(fr, 'bigint')} AND e.matches > 0 AND {sc} ORDER BY e.rank, e.last_seen_ms DESC, e.source_seq, e.dependent_seq LIMIT 5001",
        "hidden": f"SELECT DISTINCT e.dependent_seq FROM graph_derived_edges e JOIN units d ON d.unit_seq=e.dependent_seq WHERE e.source_seq {a(fr, 'bigint')} AND e.matches > 0 AND NOT {sc} LIMIT 5001",
    }


json.dump({"pg": q(True), "sq": q(False)}, open('queries.json', 'w'))
