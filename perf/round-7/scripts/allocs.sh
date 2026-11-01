#!/bin/bash
# print allocs/op and B/op of CheckpointSaveFull1000 at each listed commit
cd /tmp/bis
for c in "$@"; do git checkout -q --detach $c; r=$(go test -run '^$' -bench 'CheckpointSaveFull1000' -benchmem -benchtime 20x -count 1 ./pkg/store/sqlite/ 2>&1 | grep ^BenchmarkCheckpointSaveFull1000 | awk '{print $(NF-3), $(NF-1)}'); echo "$c $r"; done
