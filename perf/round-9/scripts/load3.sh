#!/bin/bash
S=$(cat /tmp/S); export GOFLAGS=
run(){ v=$1; i=$2
  env -u DUSTPAN_TEST_POSTGRES_URL /tmp/load-$v -out $S/raw/load-sqlite-$v-$i.json > $S/raw/load-sqlite-$v-$i.log 2>&1
  psql -h 127.0.0.1 -p 5433 -U postgres -qc "drop database if exists ld_${v}_$i" -c "create database ld_${v}_$i encoding 'UTF8' template template0 locale 'C.UTF-8'"
  DUSTPAN_TEST_POSTGRES_URL="postgres://postgres@127.0.0.1:5433/ld_${v}_$i?sslmode=disable" /tmp/load-$v -out $S/raw/load-pg-$v-$i.json > $S/raw/load-pg-$v-$i.log 2>&1
}
for i in 4 5 6 7; do
  if [ $((i%2)) = 0 ]; then run old $i; run new $i; else run new $i; run old $i; fi
done
echo done > $S/load2.done
