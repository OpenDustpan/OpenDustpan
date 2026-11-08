import sqlite3, os, sys, time, csv
d=sys.argv[1]; shape=sys.argv[2]; p=os.path.join(d,f'sq_{shape}.db')
if os.path.exists(p): os.remove(p)
db=sqlite3.connect(p)
wr = " WITHOUT ROWID" if shape=="worowid" else ""
db.executescript(f"""PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;
CREATE TABLE units (unit_seq INTEGER PRIMARY KEY, ns_seq INTEGER NOT NULL, size INTEGER NOT NULL, coll INTEGER NOT NULL);
CREATE TABLE identity_produced (ns_seq INTEGER NOT NULL, value_hash INTEGER NOT NULL, unit_seq INTEGER NOT NULL, class INTEGER NOT NULL, PRIMARY KEY (ns_seq, value_hash, unit_seq)){wr};
CREATE TABLE identity_consumed (ns_seq INTEGER NOT NULL, value_hash INTEGER NOT NULL, unit_seq INTEGER NOT NULL, PRIMARY KEY (ns_seq, value_hash, unit_seq)){wr};
CREATE TABLE graph_derived_edges (source_seq INTEGER NOT NULL, dependent_seq INTEGER NOT NULL, evidence INTEGER NOT NULL, rank INTEGER NOT NULL, matches INTEGER NOT NULL, gen_source INTEGER NOT NULL, gen_dependent INTEGER NOT NULL, first_seen_ms INTEGER NOT NULL, last_seen_ms INTEGER NOT NULL, PRIMARY KEY (source_seq, dependent_seq, evidence)){wr};
""")
def load(t,f,n):
    with open(os.path.join(d,f)) as fh:
        db.executemany(f"INSERT INTO {t} VALUES ({','.join('?'*n)})", ([int(x) for x in r] for r in csv.reader(fh)))
t=time.time()
load('units','units.csv',4); load('identity_produced','prod.csv',4); load('identity_consumed','cons.csv',3); db.commit()
print("load s", round(time.time()-t,1))
t=time.time()
db.execute("INSERT INTO graph_derived_edges SELECT p.unit_seq, c.unit_seq, 5, min(p.class), count(*), 1, 1, 1730000000000, 1730000000000 FROM identity_consumed c JOIN identity_produced p ON p.ns_seq=c.ns_seq AND p.value_hash=c.value_hash WHERE p.unit_seq<>c.unit_seq GROUP BY p.unit_seq, c.unit_seq")
db.execute("CREATE INDEX graph_derived_edges_dependent ON graph_derived_edges (dependent_seq, source_seq)"); db.commit()
print("edge build s", round(time.time()-t,2))
db.execute("ANALYZE"); db.commit()
for t in ("identity_produced","identity_consumed","graph_derived_edges"):
    n=db.execute(f"SELECT count(*) FROM {t}").fetchone()[0]
    b=db.execute("SELECT sum(pgsize) FROM dbstat WHERE tbl_name=?", (t,)).fetchone()[0] if False else None
    b=db.execute("SELECT sum(s.pgsize) FROM dbstat s JOIN sqlite_schema m ON m.name=s.name WHERE m.tbl_name=?", (t,)).fetchone()[0]
    print(shape, t, n, b, round(b/n,1))
db.close()
print("file bytes", os.path.getsize(p))
