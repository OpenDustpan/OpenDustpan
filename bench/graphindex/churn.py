import random, csv, sqlite3, subprocess, os, sys
random.seed(7)
P=[tuple(map(int,r)) for r in csv.reader(open('prod.csv'))]
C=[tuple(map(int,r)) for r in csv.reader(open('cons.csv'))]
PSQL=["psql","-qAt"]  # the connection comes from PGHOST, PGPORT, PGUSER and PGDATABASE
def pg(sql): return subprocess.run(PSQL+["-c",sql],capture_output=True,text=True).stdout.strip()
dbs={sh:sqlite3.connect(f'sq_{sh}.db') for sh in ("worowid","rowid")}
def size_report(tag):
    pgs=pg("SELECT string_agg(relname||'='||pg_total_relation_size(relid),' ' ORDER BY relname) FROM pg_stat_user_tables WHERE relname IN ('identity_produced','identity_consumed')")
    out=[f"{tag} pg {pgs}"]
    for sh,db in dbs.items():
        pc,fl=db.execute("PRAGMA page_count").fetchone()[0], db.execute("PRAGMA freelist_count").fetchone()[0]
        out.append(f"{tag} sq_{sh} file={os.path.getsize(f'sq_{sh}.db')} pages={pc} free={fl}")
    print("\n".join(out), flush=True)
size_report("cycle0")
for cyc in range(1,6):
    dp=[r for r in P if r[1]%10==cyc]; dc=[r for r in C if r[1]%10==cyc]
    ip=[(r[0],random.getrandbits(63),r[2],r[3]) for r in dp]; ic=[(r[0],random.getrandbits(63),r[2]) for r in dc]
    sp=set(dp); sc=set(dc)
    P=[r for r in P if r not in sp]+ip; C=[r for r in C if r not in sc]+ic
    for name,rows in (("dp",dp),("dc",dc),("ip",ip),("ic",ic)):
        with open(f"{name}.csv","w") as f: csv.writer(f).writerows(rows)
    sql=f"""BEGIN; CREATE TEMP TABLE dp (ns int, h bigint, u bigint, c smallint); CREATE TEMP TABLE dc (ns int, h bigint, u bigint);
\\copy dp FROM 'dp.csv' CSV
\\copy dc FROM 'dc.csv' CSV
DELETE FROM identity_produced t USING dp WHERE t.ns_seq=dp.ns AND t.value_hash=dp.h AND t.unit_seq=dp.u;
DELETE FROM identity_consumed t USING dc WHERE t.ns_seq=dc.ns AND t.value_hash=dc.h AND t.unit_seq=dc.u;
\\copy identity_produced FROM 'ip.csv' CSV
\\copy identity_consumed FROM 'ic.csv' CSV
COMMIT;"""
    open("cyc.sql","w").write(sql)
    subprocess.run(PSQL+["-f","cyc.sql"],check=True,capture_output=True)
    for db in dbs.values():
        db.executemany("DELETE FROM identity_produced WHERE ns_seq=? AND value_hash=? AND unit_seq=?", [r[:3] for r in dp])
        db.executemany("DELETE FROM identity_consumed WHERE ns_seq=? AND value_hash=? AND unit_seq=?", dc)
        db.executemany("INSERT INTO identity_produced VALUES (?,?,?,?)", ip)
        db.executemany("INSERT INTO identity_consumed VALUES (?,?,?)", ic); db.commit()
    pg("CHECKPOINT")
    size_report(f"cycle{cyc}")
subprocess.run(PSQL+["-c","VACUUM ANALYZE identity_produced, identity_consumed"])
size_report("after_vacuum")
print("rows", len(P), len(C))
