import sqlite3, random, os, time, sys
p=sys.argv[1]
if os.path.exists(p): os.remove(p)
db=sqlite3.connect(p); random.seed(1)
db.executescript("""PRAGMA page_size=4096; PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF;
CREATE TABLE prod (ns INTEGER NOT NULL, h INTEGER NOT NULL, u INTEGER NOT NULL, strength INTEGER NOT NULL, PRIMARY KEY (ns,h,u)) WITHOUT ROWID;
CREATE TABLE cons (ns INTEGER NOT NULL, h INTEGER NOT NULL, u INTEGER NOT NULL, PRIMARY KEY (ns,h,u)) WITHOUT ROWID;
CREATE TABLE prodtext (namespace_id TEXT NOT NULL, value TEXT NOT NULL, tool TEXT NOT NULL, unit_id TEXT NOT NULL, PRIMARY KEY (namespace_id,value,tool,unit_id)) WITHOUT ROWID;
CREATE TABLE edges (src INTEGER NOT NULL, dep INTEGER NOT NULL, kind INTEGER NOT NULL, evidence INTEGER NOT NULL, matches INTEGER NOT NULL, last_seen_ms INTEGER NOT NULL, PRIMARY KEY (src,dep,kind,evidence)) WITHOUT ROWID;
CREATE INDEX edges_dep ON edges(dep, src);""")
R=lambda: random.getrandbits(63)
prod={}
rows=[]
for u in range(1,3335):
    hs=[R() for _ in range(600)]; prod[u]=hs
    rows += [(u%10,h,u,1) for h in hs]
db.executemany("INSERT INTO prod VALUES (?,?,?,?)", rows)
rows=[]
for u in range(35,3335):
    s=set(random.sample(prod[(u%34)+1],315))
    for i in range(135): s.add(prod[random.randint(1,3334)][random.randint(0,599)])
    rows += [(u%10,h,u) for h in s]
db.executemany("INSERT OR IGNORE INTO cons VALUES (?,?,?)", rows)
import hashlib
db.executemany("INSERT OR IGNORE INTO prodtext VALUES (?,?,?,?)", (("ns_01JABCDEF0123456789ABCD%d"%(u%10), "arn:aws:ec2:eu-west-1:123456789012:subnet/subnet-"+hashlib.md5(b"%d:%d"%(u,k)).hexdigest()[:17], "opentofu", "ws-01JABCDEF0123456789%05d"%u) for u in range(1,3335) for k in range(600)))
db.commit()
t=time.time()
db.execute("INSERT OR IGNORE INTO edges SELECT p.u, c.u, 2, 5, count(*), 1730000000000 FROM cons c JOIN prod p ON p.h=c.h AND p.ns=c.ns WHERE p.u<>c.u GROUP BY p.u,c.u"); db.commit()
print("edge build s", round(time.time()-t,2))
for name in ("prod","cons","prodtext","edges","edges_dep"):
    n=db.execute("SELECT count(*) FROM "+("edges" if name=="edges_dep" else name)).fetchone()[0]
    b=db.execute("SELECT sum(pgsize) FROM dbstat WHERE name=?", (name,)).fetchone()[0]
    print(name, n, b, round(b/n,1))
hs=prod[1][:1000]
t=time.time()
q="SELECT u, count(*) FROM cons WHERE ns=1 AND h IN (%s) GROUP BY u"%",".join("?"*len(hs))
r=db.execute(q,hs).fetchall(); print("probe ms", round((time.time()-t)*1000,2), len(r))
print(db.execute("EXPLAIN QUERY PLAN "+q,hs).fetchall())
fr=list(range(1,501)); t=time.time()
q="SELECT src,dep,kind,evidence,matches FROM edges WHERE src IN (%s) AND last_seen_ms>=0 ORDER BY last_seen_ms DESC, src, dep LIMIT 5001"%",".join("?"*500)
r=db.execute(q,fr).fetchall(); print("hop ms", round((time.time()-t)*1000,2), len(r))
print(db.execute("EXPLAIN QUERY PLAN "+q,fr).fetchall())
