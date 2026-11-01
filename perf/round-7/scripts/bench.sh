#!/bin/bash
# Alternating old/new Go benchmarks, 6 rounds each. Scope: make bench scope plus the packages with hot-path benchmarks.
cd /tmp/perf7/raw
export DUSTPAN_TEST_POSTGRES_URL='postgres://postgres@127.0.0.1:5433/postgres?sslmode=disable'
PK="./pkg/stack/ ./pkg/store/sqlite/ ./pkg/store/postgres/ ./pkg/dustpan/ ./pkg/workspace/ ./pkg/ipallow/ ./internal/pulumijournal/"
for i in 1 2 3 4 5 6; do
  for v in old new; do
    (cd /tmp/$v && go test -run '^$' -bench . -benchmem -benchtime 300ms -count 1 -timeout 60m $PK 2>&1) >> /tmp/perf7/raw/bench-$v.txt
  done
done
echo done > /tmp/perf7/bench.done
