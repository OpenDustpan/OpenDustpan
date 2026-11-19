#!/usr/bin/env bash
# Run the soak run against PostgreSQL and write perf-out/soak/measurements.json.
# Needs DUSTPAN_TEST_POSTGRES_URL. SOAK_DURATION defaults to 30m (make soak).
set -euo pipefail
cd "$(dirname "$0")/.."
. perf/lib.sh
perf_require DUSTPAN_TEST_POSTGRES_URL
mkdir -p "$PERF_OUT/soak"
report_dir="$(cd "$PERF_OUT/soak" && pwd)/reports"
make soak SOAK_REPORT_DIR="$report_dir"
csv=$(find "$report_dir" -name 'soak-postgres-*.csv' | sort | tail -n 1)
if [ -z "$csv" ]; then
	echo "perf: the soak run wrote no CSV in $report_dir" >&2
	exit 1
fi
perf_compare extract-soak <"$csv" >"$PERF_OUT/soak/measurements.json"
