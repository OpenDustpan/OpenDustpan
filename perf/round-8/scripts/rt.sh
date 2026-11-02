#!/bin/bash
S=$(cat /tmp/S); export DUSTPAN_TEST_POSTGRES_URL='postgres://postgres@127.0.0.1:5433/postgres?sslmode=disable'
for i in 1 2; do for v in old new; do
 (cd /tmp/$v && go test -count=1 -v -run 'TestRoundTrips' -timeout 30m ./internal/servertest/ 2>&1) > $S/raw/roundtrips-$v-$i.txt
done; done; echo done > $S/rt.done
