#!/bin/bash
S=/home/user/OpenDustpan/perf/round-11/raw; W=/tmp/claude-0
export DUSTPAN_TEST_POSTGRES_URL='postgres://postgres@127.0.0.1:5433/postgres?sslmode=disable'
ord(){ if [ $(($1%2)) = 1 ]; then echo "old new"; else echo "new old"; fi; }
for i in 1 2 3 4 5; do for v in $(ord $i); do (cd $W/$v && go test -count=1 -v -run 'TestPerf11(OIDC|Password)' ./internal/api/server/ 2>&1) > $S/signin-$v-$i.txt; done; done
echo signin >> $W/progress.log
run(){ v=$1; i=$2
  env -u DUSTPAN_TEST_POSTGRES_URL $W/load-$v -out $S/load-sqlite-$v-$i.json > $S/load-sqlite-$v-$i.log 2>&1
  psql -h 127.0.0.1 -p 5433 -U postgres -qc "drop database if exists ld_${v}_$i" -c "create database ld_${v}_$i encoding 'UTF8' template template0 locale 'C.UTF-8'"
  DUSTPAN_TEST_POSTGRES_URL="postgres://postgres@127.0.0.1:5433/ld_${v}_$i?sslmode=disable" $W/load-$v -out $S/load-pg-$v-$i.json > $S/load-pg-$v-$i.log 2>&1
}
for i in 1 2 3 4 5; do if [ $((i%2)) = 1 ]; then run old $i; run new $i; else run new $i; run old $i; fi; done
echo load2 >> $W/progress.log
