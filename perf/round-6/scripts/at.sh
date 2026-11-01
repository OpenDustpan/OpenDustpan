#!/bin/bash
# usage: at.sh <commit> : bench DeltaApply1M and CheckpointSaveFull1000 (median of 8 runs) at a commit
c=$1; w=/tmp/bis; cd /home/user/OpenDustpan && { git worktree add -f --detach $w $c >/dev/null 2>&1 || (cd $w && git checkout -q --detach $c); }
cd $w && for i in 1 2 3 4 5 6 7 8; do go test -run '^$' -bench 'DeltaApply1M|CheckpointSaveFull1000' -benchtime 300ms -count 1 ./pkg/stack/ ./pkg/store/sqlite/ 2>&1 | grep ^Benchmark; done > /tmp/perf6/at.out
for n in DeltaApply1M CheckpointSaveFull1000; do
  grep "^Benchmark$n-" /tmp/perf6/at.out | awk '{print $3}' | sort -n | awk -v c="$c" -v n=$n '{a[NR]=$1} END{print c, n, a[int((NR+1)/2)], "ns/op"}'
done
