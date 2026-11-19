#!/usr/bin/env bash
# Run the load harness against PostgreSQL and write perf-out/load/measurements.json.
# Needs DUSTPAN_TEST_POSTGRES_URL (the PostgreSQL service container).
# PERF_LOAD_ARGS adds flags of the load harness, split on spaces.
set -euo pipefail
cd "$(dirname "$0")/.."
. perf/lib.sh
perf_require DUSTPAN_TEST_POSTGRES_URL
mkdir -p "$PERF_OUT/load"
read -r -a extra <<<"${PERF_LOAD_ARGS:-}"
go run ./load "${extra[@]}" -out "$PERF_OUT/load/load-report.json"
perf_compare extract-load <"$PERF_OUT/load/load-report.json" >"$PERF_OUT/load/measurements.json"
