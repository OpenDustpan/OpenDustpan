#!/bin/bash
# Alternating old/new Go benchmarks (make bench scope), 6 rounds each, same machine.
cd /tmp/perf6/raw
for i in 1 2 3 4 5 6; do
  for v in old new; do
    (cd /tmp/$v && go test -run '^$' -bench . -benchmem -benchtime 300ms -count 1 -timeout 30m ./pkg/stack/ ./pkg/store/sqlite/ 2>&1) >> /tmp/perf6/raw/bench-$v.txt
  done
done
