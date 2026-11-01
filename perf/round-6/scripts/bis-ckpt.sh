#!/bin/bash
# exit 1 (bad) if median CheckpointSaveFull1000 > 130us
cd /tmp/bis || exit 125
m=$(for i in 1 2 3 4 5 6 7; do go test -run '^$' -bench 'CheckpointSaveFull1000' -benchtime 300ms -count 1 ./pkg/store/sqlite/ 2>&1 | grep ^BenchmarkCheckpointSaveFull1000 | awk '{print $3}'; done | sort -n | sed -n 4p)
a=$(go test -run '^$' -bench 'CheckpointSaveFull1000' -benchmem -benchtime 20x -count 1 ./pkg/store/sqlite/ 2>&1 | grep ^BenchmarkCheckpointSaveFull1000 | awk '{print $(NF-1)}')
echo "$(git rev-parse --short HEAD) $m allocs=$a" >> /tmp/perf6/bisect-ckpt.log
[ -z "$m" ] && exit 125
[ "$m" -gt 130000 ] && exit 1 || exit 0
