#!/bin/bash
S=/home/user/OpenDustpan/perf/round-11/raw; W=/tmp/claude-0
for i in 1 2 3 4 5 6; do
 if [ $((i%2)) = 1 ]; then vs="old new"; else vs="new old"; fi
 for v in $vs; do env -u DUSTPAN_TEST_POSTGRES_URL $W/load-$v -out $S/load-sqlr-$v-$i.json > $S/load-sqlr-$v-$i.log 2>&1; done
done; echo loadr >> $W/progress.log
