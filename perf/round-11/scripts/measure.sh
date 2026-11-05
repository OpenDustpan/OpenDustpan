#!/bin/bash
# round 11 measurements, sequential, order alternated. old=519b3bd new=b4e1757
S=/home/user/OpenDustpan/perf/round-11/raw; W=/tmp/claude-0
export DUSTPAN_TEST_POSTGRES_URL='postgres://postgres@127.0.0.1:5433/postgres?sslmode=disable'
ord(){ if [ $(($1%2)) = 1 ]; then echo "old new"; else echo "new old"; fi; }
t(){ echo "$(date +%T) $*" >> $W/progress.log; }
t rmtrips
for v in old new; do (cd $W/$v && go test -count=1 -v -run TestPerf11RmTrips ./internal/servertest/ 2>&1) > $S/rmtrips-$v.txt; done
t signin
for i in 1 2 3 4 5; do for v in $(ord $i); do (cd $W/$v && go test -count=1 -v -run 'TestPerf11(OIDC|Password)' ./internal/api/server/ 2>&1) > $S/signin-$v-$i.txt; done; done
t proxybench
for i in 1 2 3; do for v in $(ord $i); do (cd $W/$v && go test -count=1 -run '^$' -bench 'Perf11|IPAllowlistServeNamespaceRead' -benchtime=20000x -count=3 ./pkg/dustpan/ 2>&1) > $S/proxybench-$v-$i.txt; done; done
t pgopen
for i in 1 2 3; do for v in $(ord $i); do (cd $W/$v && go test -count=1 -v -run TestPerf11Migrate ./pkg/store/postgres/ 2>&1) > $S/pgopen-$v-$i.txt; done; done
t latency
for i in 1 2 3 4 5 6 7; do for v in $(ord $i); do (cd $W/$v && go test -count=1 -v -run 'TestPerf11Latency' -timeout 30m ./internal/servertest/ 2>&1) > $S/lat-$v-$i.txt; done; done
t contend
for i in 1 2 3 4; do for v in $(ord $i); do (cd $W/$v && go test -count=1 -v -run 'TestPerf11Contend' -timeout 30m ./internal/servertest/ 2>&1) > $S/contend-$v-$i.txt; done; done
t backup
for i in 1 2; do for v in $(ord $i); do (cd $W/$v && go test -count=1 -v -run 'TestPerf11Backup' -timeout 30m ./internal/servertest/ 2>&1) > $S/backup-$v-$i.txt; done; done
t done; echo done > $W/measure.done
