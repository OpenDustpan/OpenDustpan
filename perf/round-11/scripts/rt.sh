#!/bin/bash
# round-trip tests, 8 runs per commit, order alternated. old=519b3bd new=b4e1757
S=/home/user/OpenDustpan/perf/round-11; export DUSTPAN_TEST_POSTGRES_URL='postgres://postgres@127.0.0.1:5433/postgres?sslmode=disable'
for i in 1 2 3 4 5 6 7 8; do
 if [ $((i%2)) = 1 ]; then vs="old new"; else vs="new old"; fi
 for v in $vs; do
  (cd /tmp/claude-0/$v && go test -count=1 -v -run 'TestRoundTrips' -timeout 30m ./internal/servertest/ 2>&1) > $S/raw/roundtrips-$v-$i.txt
 done
done; echo done > /tmp/claude-0/rt.done
