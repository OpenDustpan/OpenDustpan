#!/bin/bash
# PostgreSQL-only load re-run, files load-pgr-*
S=/tmp/claude-0/r10
run(){ v=$1; i=$2
  psql -h 127.0.0.1 -p 5433 -U postgres -qc "drop database if exists lr_${v}_$i" -c "create database lr_${v}_$i encoding 'UTF8' template template0 locale 'C.UTF-8'"
  DUSTPAN_TEST_POSTGRES_URL="postgres://postgres@127.0.0.1:5433/lr_${v}_$i?sslmode=disable" /tmp/load-$v -out $S/raw/load-pgr-$v-$i.json > $S/raw/load-pgr-$v-$i.log 2>&1
}
for i in 1 2 3 4 5; do
  if [ $((i%2)) = 1 ]; then run new $i; run old $i; else run old $i; run new $i; fi
done
echo done > $S/loadr.done
