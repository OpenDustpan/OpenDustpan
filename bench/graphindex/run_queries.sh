#!/bin/bash
# Usage: run_queries.sh cold|warm DATA_DIR
# Runs each query of queries.json in PostgreSQL and in both SQLite files, and
# prints the time and the row count. "cold" restarts PostgreSQL and drops the
# OS page cache before each query: it needs root, and PGDATA set to the data
# directory of a server that the user postgres runs. The connection comes
# from PGHOST, PGPORT, PGUSER and PGDATABASE.
set -euo pipefail
mode=$1
cd "$2"
cold() {
  su postgres -c "pg_ctl -D '$PGDATA' -m fast restart -l '$PGDATA/log'" >/dev/null
  sync
  echo 3 > /proc/sys/vm/drop_caches
}
for k in probe down up hidden; do
  q=$(python3 -I -c "import json;print(json.load(open('queries.json'))['pg']['$k'])")
  if [ "$mode" = cold ]; then cold; fi
  ms=$(psql -qAt -c '\timing on' -c "SELECT count(*) FROM ($q) x" 2>&1 | tr '\n' ' ')
  echo "pg $mode $k: $ms"
  for sh in worowid rowid; do
    if [ "$mode" = cold ]; then sync; echo 3 > /proc/sys/vm/drop_caches; fi
    python3 -I -c "
import json,sqlite3,time
q=json.load(open('queries.json'))['sq']['$k']
db=sqlite3.connect('sq_$sh.db'); t=time.perf_counter(); n=len(db.execute(q).fetchall()); print('sq_$sh $mode $k:', n, round((time.perf_counter()-t)*1000,2),'ms')"
  done
done
