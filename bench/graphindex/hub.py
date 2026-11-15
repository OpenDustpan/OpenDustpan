# A hub run: unit 1 produces 60,000 values that about 2,000 units consume.
# Read probes in batches of 1,000 hashes, then edge upserts in batches of
# 1,000 rows, each its own short write transaction, with a generation check.
import sqlite3, time, json, subprocess, sys
hs=[int(l.split(',')[1]) for l in open('prod.csv') if l.split(',')[2]=='1']
PSQL=["psql","-qAt"]  # the connection comes from PGHOST, PGPORT, PGUSER and PGDATABASE
UPS="""INSERT INTO graph_derived_edges (source_seq,dependent_seq,evidence,rank,matches,gen_source,gen_dependent,first_seen_ms,last_seen_ms)
VALUES {vals}
ON CONFLICT (source_seq,dependent_seq,evidence) DO UPDATE SET matches=excluded.matches, rank=excluded.rank,
 gen_source=excluded.gen_source, gen_dependent=excluded.gen_dependent, last_seen_ms=excluded.last_seen_ms
WHERE excluded.gen_source >= graph_derived_edges.gen_source AND excluded.gen_dependent >= graph_derived_edges.gen_dependent"""
def run(dialect, db=None):
    counts={}; probe_ms=[]
    for i in range(0,len(hs),1000):
        b=hs[i:i+1000]
        t=time.perf_counter()
        if dialect=="pg":
            out=subprocess.run(PSQL+["-c",f"SELECT unit_seq, count(*) FROM identity_consumed WHERE ns_seq=0 AND value_hash = ANY(ARRAY(SELECT jsonb_array_elements_text('{json.dumps(b)}'::jsonb)::bigint)) GROUP BY unit_seq"],capture_output=True,text=True).stdout.split()
            rows=[tuple(map(int,x.split('|'))) for x in out]
        else:
            rows=db.execute("SELECT unit_seq, count(*) FROM identity_consumed WHERE ns_seq=0 AND value_hash IN (SELECT value FROM json_each(?)) GROUP BY unit_seq",(json.dumps(b),)).fetchall()
        probe_ms.append((time.perf_counter()-t)*1000)
        for u,c in rows: counts[u]=counts.get(u,0)+c
    deps=sorted(counts.items()); up_ms=[]
    for i in range(0,len(deps),1000):
        vals=",".join(f"(1,{u},5,1,{c},2,1,1730000000000,1730000099999)" for u,c in deps[i:i+1000])
        t=time.perf_counter()
        if dialect=="pg":
            subprocess.run(PSQL+["-c",UPS.format(vals=vals)],check=True,capture_output=True)
        else:
            db.execute("BEGIN IMMEDIATE"); db.execute(UPS.format(vals=vals)); db.execute("COMMIT")
        up_ms.append((time.perf_counter()-t)*1000)
    print(f"{dialect}: probes {len(probe_ms)} total {sum(probe_ms):.0f} ms max {max(probe_ms):.1f} ms; consumers {len(deps)}; upsert txns {len(up_ms)} max {max(up_ms):.1f} ms total {sum(up_ms):.0f} ms")
run("pg")
for sh in ("worowid","rowid"):
    db=sqlite3.connect(f"sq_{sh}.db", isolation_level=None); print(sh, end=" "); run("sq", db)
