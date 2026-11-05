#!/bin/bash
S=/home/user/OpenDustpan/perf/round-11/raw; W=/tmp/claude-0
export DUSTPAN_TEST_POSTGRES_URL='postgres://postgres@127.0.0.1:5433/postgres?sslmode=disable'
for i in 3 4 5 6 7 8; do if [ $((i%2)) = 1 ]; then vs="old new"; else vs="new old"; fi
 for v in $vs; do (cd $W/$v && go test -count=1 -v -run TestPerf11Backup -timeout 30m ./internal/servertest/ 2>&1) > $S/backup-$v-$i.txt; done; done
echo restorerep >> $W/progress.log
