#!/bin/bash
# exit 1 (bad) if median DeltaApply1M > 490us
cd /tmp/bis || exit 125
m=$(for i in 1 2 3 4 5 6 7; do go test -run '^$' -bench 'DeltaApply1M' -benchtime 300ms -count 1 ./pkg/stack/ 2>&1 | grep ^BenchmarkDeltaApply1M | awk '{print $3}'; done | sort -n | sed -n 4p)
echo "$(git rev-parse --short HEAD) $m" >> /tmp/perf6/bisect-delta.log
[ -z "$m" ] && exit 125
[ "$m" -gt 490000 ] && exit 1 || exit 0
