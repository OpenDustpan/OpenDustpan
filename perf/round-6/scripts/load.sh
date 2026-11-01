#!/bin/bash
# standard load flows, 3 alternating rounds, sqlite then postgres (fresh db each run)
export GOFLAGS=
cd /tmp/perf6/raw
for i in 1 2 3; do for v in old new; do
  (cd /tmp/$v && go build -o /tmp/load-$v ./load) || continue
  env -u DUSTPAN_TEST_POSTGRES_URL /tmp/load-$v -out /tmp/perf6/raw/load-sqlite-$v-$i.json > /tmp/perf6/raw/load-sqlite-$v-$i.log 2>&1
  psql -h 127.0.0.1 -p 5433 -U postgres -qc "drop database if exists ld_${v}_$i" -c "create database ld_${v}_$i encoding 'UTF8' template template0 locale 'C.UTF-8'"
  DUSTPAN_TEST_POSTGRES_URL="postgres://postgres@127.0.0.1:5433/ld_${v}_$i?sslmode=disable" /tmp/load-$v -out /tmp/perf6/raw/load-pg-$v-$i.json > /tmp/perf6/raw/load-pg-$v-$i.log 2>&1
done; done
echo done > /tmp/perf6/load.done
