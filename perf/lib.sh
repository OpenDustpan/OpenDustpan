# Shared by run-load.sh, run-soak.sh and run-toxiproxy.sh. Source it; do not run it.
# PERF_OUT is the output directory (default perf-out). Each wrapper writes
# $PERF_OUT/<scope>/measurements.json, the artifact that the compare program reads.

PERF_OUT="${PERF_OUT:-perf-out}"

perf_require() {
	local name
	for name in "$@"; do
		if [ -z "${!name:-}" ]; then
			echo "perf: set $name" >&2
			exit 1
		fi
	done
}

perf_compare() {
	go run ./cmd/perfcompare "$@"
}
