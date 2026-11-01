#!/bin/bash
# scale flows: 1 and 2 instances, old and new server binary, same harness (built from main), 2 alternating rounds
cd /tmp/perf6/raw
export DUSTPAN_TEST_S3_ENDPOINT=http://127.0.0.1:9000 DUSTPAN_TEST_S3_BUCKET=perf6 DUSTPAN_TEST_S3_ACCESS_KEY=<local-minio-user> DUSTPAN_TEST_S3_SECRET_KEY=<local-minio-password>
for r in 1 2; do for n in 1 2; do for v in old new; do
  db=sc_${v}_${n}_$r
  psql -h 127.0.0.1 -p 5433 -U postgres -qc "drop database if exists $db" -c "create database $db encoding 'UTF8' template template0 locale 'C.UTF-8'"
  DUSTPAN_TEST_POSTGRES_URL="postgres://postgres@127.0.0.1:5433/$db?sslmode=disable" /tmp/load-scale -flows scale -binary $( [ $v = old ] && echo /tmp/bin-old-wrap || echo /tmp/bin-new ) -instances $n -clients 32 -units 500 -warmup 5s -duration 30s -out /tmp/perf6/raw/scale-$v-$n-$r.json > /tmp/perf6/raw/scale-$v-$n-$r.log 2>&1
done; done; done
echo done > /tmp/perf6/scale.done
