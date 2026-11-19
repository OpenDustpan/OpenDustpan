#!/usr/bin/env bash
# Run the Toxiproxy fault tests against the Toxiproxy service container and write
# perf-out/toxiproxy/measurements.json. Needs TOXIPROXY_URL and
# DUSTPAN_TEST_POSTGRES_URL.
set -euo pipefail
cd "$(dirname "$0")/.."
. perf/lib.sh
perf_require TOXIPROXY_URL DUSTPAN_TEST_POSTGRES_URL
mkdir -p "$PERF_OUT/toxiproxy"
json="$PERF_OUT/toxiproxy/faults.json"
# A failing test fails the step; the JSON stays for the log.
go test -tags faults -count=1 -timeout 20m -json ./e2e/faults/... >"$json"
perf_compare extract-faults <"$json" >"$PERF_OUT/toxiproxy/measurements.json"
