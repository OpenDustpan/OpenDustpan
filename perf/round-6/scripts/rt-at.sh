#!/bin/bash
c=$1; cd /tmp/old && git reset -q --hard && git clean -qfd && git checkout -q --detach $c || exit 1
if ! [ -f internal/servertest/roundtrips_test.go ]; then git cherry-pick --no-commit bab1cc24 >/dev/null 2>&1 || { echo "$c graft conflict"; git reset -q --hard; exit 1; }; fi
[ -f internal/servertest/unsupported_detail_test.go ] || cp /tmp/perf6/zz_shim_test.go internal/servertest/zz_shim_test.go
export DUSTPAN_TEST_POSTGRES_URL='postgres://postgres@127.0.0.1:5433/postgres?sslmode=disable'
go test -count=1 -run 'TestRoundTripsOfBackup' -v ./internal/servertest/ 2>&1 | grep -E "(backup|restore) of 3.* round trips" | sed "s/^ *roundtrips_test.go:[0-9]*: /$c /"
