#!/usr/bin/env bash
# Run the S3 latency benchmark (make bench-s3).
#
# It starts MinIO, Garage, PostgreSQL 18 and Toxiproxy as containers pinned
# by digest on a private Docker network, writes throwaway credentials to a
# private temporary directory, builds the load harness, and runs
# cmd/s3bench. It removes the containers, the network and the
# credentials at the end, also after a failure.
#
# MinIO enforces If-None-Match on PUT. Garage ignores it. Toxiproxy sits
# between the benchmark and each store, and between the benchmark and
# PostgreSQL, and adds the delay of each network profile.
#
# Environment:
#   BENCH_S3_OUT    output directory (default bench-s3-out)
#   BENCH_S3_ARGS   extra arguments for cmd/s3bench, for example
#                   "-quick" or "-profiles eu-metro -limits"
#   BENCH_S3_NAME   name prefix of the containers and the network
#                   (default odp-bench-s3)
#   BENCH_S3_MODE   empty for the profile run, or "protocol" for the
#                   protocol run of the blob families (make bench-s3-protocol)
#   BENCH_S3_PROTOCOL_PROFILES
#                   profiles of the protocol run, split by spaces
#                   (default "same-az cross-az eu-metro")
#   BENCH_S3_MINIO_TMPFS
#                   size of a tmpfs for the MinIO data, for example 8g
#                   (default: no tmpfs, the data is on the disk)
#   BENCH_S3_PORT   first host port; the run uses this port (Toxiproxy API)
#                   and the next three (MinIO, Garage, PostgreSQL)
#                   (default 18474)
#
# Docker must run. The script publishes ports on 127.0.0.1 only. The
# Toxiproxy API has no authentication, so it must stay on 127.0.0.1.
set -euo pipefail

# Images, pinned by digest. MinIO no longer publishes images to Docker Hub
# or Quay, so the run uses the Chainguard build of MinIO.
MINIO_IMAGE="cgr.dev/chainguard/minio@sha256:e70b2b1e74d79457f808710483bd726ff364529ca30736eaa15a6ab547d3a5a7"
GARAGE_IMAGE="dxflrs/garage@sha256:4c9b34c113e61358466e83fd6e7d66e6d18657ede14b776eb78a93ee8da7cf6a"
POSTGRES_IMAGE="mirror.gcr.io/library/postgres@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6"
TOXIPROXY_IMAGE="ghcr.io/shopify/toxiproxy@sha256:9378ed52a28bc50edc1350f936f518f31fa95f0d15917d6eb40b8e376d1a214e"

OUT_DIR="${BENCH_S3_OUT:-bench-s3-out}"
NAME="${BENCH_S3_NAME:-odp-bench-s3}"
PORT="${BENCH_S3_PORT:-18474}"
read -r -a EXTRA_ARGS <<<"${BENCH_S3_ARGS:-}"

case "$NAME" in
*[!a-z0-9-]* | "")
	echo "bench-s3: BENCH_S3_NAME must hold only a-z, 0-9 and -" >&2
	exit 1
	;;
esac

case "$PORT" in
*[!0-9]* | "")
	echo "bench-s3: BENCH_S3_PORT must be a port number" >&2
	exit 1
	;;
esac

API_PORT="$PORT"
MINIO_PORT=$((PORT + 1))
GARAGE_PORT=$((PORT + 2))
PG_PORT=$((PORT + 3))

NET="$NAME-net"
WORK="$(mktemp -d)"
chmod 700 "$WORK"
SECRETS="$WORK/secrets"
mkdir -m 700 "$SECRETS"

TRASH_PID=""

# empty_minio_trash deletes the content of the MinIO trash directory every
# 10 seconds until the run ends. The trash holds objects that MinIO deleted
# already, so this frees space and changes no object.
empty_minio_trash() {
	while sleep 10; do
		docker exec "$NAME-minio" sh -c 'rm -rf /tmp/data/.minio.sys/tmp/.trash/*' >/dev/null 2>&1 || true
	done
}

remove_containers() {
	docker rm -f "$NAME-toxiproxy" "$NAME-minio" "$NAME-garage" "$NAME-postgres" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
}

cleanup() {
	if [ -n "$TRASH_PID" ]; then
		kill "$TRASH_PID" 2>/dev/null || true
	fi
	remove_containers
	rm -rf "$WORK"
}
trap cleanup EXIT
# Exit on a signal too, so that the EXIT trap removes the containers.
trap 'exit 130' INT
trap 'exit 143' TERM

# random_hex prints n random bytes as hex. The value goes to a file, never
# to the terminal.
random_hex() {
	od -An -tx1 -N"$1" /dev/urandom | tr -d ' \n'
}

# write_secret writes one credential file. The directory is 0700, so no
# other host user reaches the file. The file itself is 0644, so the user of
# each container can read its bind-mounted copy.
write_secret() {
	printf '%s' "$2" >"$SECRETS/$1"
	chmod 644 "$SECRETS/$1"
}

for tool in docker go od; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "bench-s3: $tool is not installed" >&2
		exit 1
	fi
done

# Throwaway credentials. Garage takes a key ID of GK and 24 hex digits and
# a secret of 64 hex digits.
write_secret minio-access "bench$(random_hex 8)"
write_secret minio-secret "$(random_hex 24)"
write_secret garage-access "GK$(random_hex 12)"
write_secret garage-secret "$(random_hex 32)"
write_secret pg-password "$(random_hex 24)"

# A run that was killed may have left its containers behind.
remove_containers
docker network create "$NET" >/dev/null

echo "bench-s3: start PostgreSQL 18"
docker run -d --name "$NAME-postgres" --network "$NET" --network-alias postgres \
	-v "$SECRETS/pg-password:/run/secrets/pg-password:ro" \
	-e POSTGRES_USER=bench -e POSTGRES_DB=bench \
	-e POSTGRES_PASSWORD_FILE=/run/secrets/pg-password \
	"$POSTGRES_IMAGE" >/dev/null

# MinIO moves a deleted object to a trash directory and empties it later.
# A protocol run writes and deletes gigabytes faster than MinIO empties the
# trash, and MinIO then answers 507. empty_minio_trash below deletes the
# trash every 10 seconds. BENCH_S3_MINIO_TMPFS puts the MinIO data on a
# tmpfs of that size instead of the disk. The tmpfs uses memory.
MINIO_TMPFS=()
if [ -n "${BENCH_S3_MINIO_TMPFS:-}" ]; then
	MINIO_TMPFS=(--tmpfs "/tmp/data:rw,size=$BENCH_S3_MINIO_TMPFS")
fi
echo "bench-s3: start MinIO"
docker run -d --name "$NAME-minio" --network "$NET" --network-alias minio \
	-v "$SECRETS/minio-access:/run/secrets/minio-access:ro" \
	-v "$SECRETS/minio-secret:/run/secrets/minio-secret:ro" \
	-e MINIO_ROOT_USER_FILE=/run/secrets/minio-access \
	-e MINIO_ROOT_PASSWORD_FILE=/run/secrets/minio-secret \
	"${MINIO_TMPFS[@]+"${MINIO_TMPFS[@]}"}" \
	"$MINIO_IMAGE" server /tmp/data >/dev/null

echo "bench-s3: start Garage"
GARAGE_CONF="$SECRETS/garage.toml"
(
	cat >"$GARAGE_CONF" <<EOF
metadata_dir = "/tmp/garage/meta"
data_dir = "/tmp/garage/data"
db_engine = "sqlite"
replication_factor = 1
rpc_bind_addr = "0.0.0.0:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret = "$(random_hex 32)"

[s3_api]
s3_region = "us-east-1"
api_bind_addr = "0.0.0.0:3900"
EOF
)
chmod 644 "$GARAGE_CONF"
docker run -d --name "$NAME-garage" --network "$NET" --network-alias garage \
	-v "$GARAGE_CONF:/etc/garage.toml:ro" \
	"$GARAGE_IMAGE" >/dev/null

echo "bench-s3: start Toxiproxy"
docker run -d --name "$NAME-toxiproxy" --network "$NET" \
	-p "127.0.0.1:$API_PORT:8474" \
	-p "127.0.0.1:$MINIO_PORT:$MINIO_PORT" \
	-p "127.0.0.1:$GARAGE_PORT:$GARAGE_PORT" \
	-p "127.0.0.1:$PG_PORT:$PG_PORT" \
	"$TOXIPROXY_IMAGE" -host 0.0.0.0 >/dev/null

garage() {
	docker exec "$NAME-garage" /garage "$@" 2>/dev/null
}

echo "bench-s3: set up the Garage layout, bucket and key"
node=""
for _ in $(seq 1 30); do
	node="$(garage node id -q | cut -d@ -f1 || true)"
	[ -n "$node" ] && break
	sleep 1
done
if [ -z "$node" ]; then
	echo "bench-s3: Garage did not start; run docker logs $NAME-garage" >&2
	exit 1
fi
garage layout assign -z dc1 -c 1G "$node" >/dev/null
garage layout apply --version 1 >/dev/null
garage bucket create bench >/dev/null
garage key import --yes -n bench "$(cat "$SECRETS/garage-access")" "$(cat "$SECRETS/garage-secret")" >/dev/null
garage bucket allow --read --write --owner bench --key bench >/dev/null

echo "bench-s3: wait for PostgreSQL"
for _ in $(seq 1 60); do
	if docker exec "$NAME-postgres" pg_isready -U bench -d bench >/dev/null 2>&1; then
		break
	fi
	sleep 1
done

echo "bench-s3: build the benchmark and the load harness"
go build -o "$WORK/s3bench" ./cmd/s3bench
go build -o "$WORK/load" ./load

# The record names each store and its version, so a missing MinIO version
# stops the run.
MINIO_VERSION="$(docker exec "$NAME-minio" minio --version 2>/dev/null | head -1 || true)"
if [ -z "$MINIO_VERSION" ]; then
	echo "bench-s3: cannot read the MinIO version; run docker logs $NAME-minio" >&2
	exit 1
fi
GARAGE_VERSION="$(docker exec "$NAME-garage" /garage --version 2>/dev/null | head -1 || true)"
if [ -z "$GARAGE_VERSION" ]; then
	echo "bench-s3: cannot read the Garage version; run docker logs $NAME-garage" >&2
	exit 1
fi
POSTGRES_VERSION="$(docker exec "$NAME-postgres" postgres --version 2>/dev/null | head -1 || true)"
if [ -z "$POSTGRES_VERSION" ]; then
	echo "bench-s3: cannot read the PostgreSQL version; run docker logs $NAME-postgres" >&2
	exit 1
fi

mkdir -p "$OUT_DIR/load"
{
	echo "minio=$MINIO_IMAGE"
	echo "garage=$GARAGE_IMAGE"
	echo "postgres=$POSTGRES_IMAGE"
	echo "toxiproxy=$TOXIPROXY_IMAGE"
	docker version --format 'docker={{.Server.Version}}'
	echo "minio-version=$MINIO_VERSION"
	echo "garage-version=$GARAGE_VERSION"
	echo "postgres-version=$POSTGRES_VERSION"
} >"$OUT_DIR/images.txt"

COMMON=(
	-out "$OUT_DIR" -secrets "$SECRETS"
	-toxiproxy "http://127.0.0.1:$API_PORT"
	-minio-port "$MINIO_PORT" -garage-port "$GARAGE_PORT" -pg-port "$PG_PORT"
)

bench() {
	"$WORK/s3bench" "$1" "${COMMON[@]}" ${EXTRA_ARGS[@]+"${EXTRA_ARGS[@]}"} "${@:2}"
}

# run_load runs the blob flows of the load harness for one step on one
# store, with the server's database and blob store behind the proxies. The
# credentials go to the harness in its environment only. A failed run leaves
# its log for the render step and does not stop the benchmark.
run_load() {
	local step="$1" store="$2" reps="$3" dataset="$4" duration="$5" probe=false port
	case "$store" in
	minio)
		port="$MINIO_PORT"
		probe=true
		;;
	garage) port="$GARAGE_PORT" ;;
	*)
		echo "bench-s3: unknown store $store" >&2
		return 1
		;;
	esac
	echo "bench-s3: load harness, $step on $store"
	if ! DUSTPAN_TEST_S3_ENDPOINT="http://127.0.0.1:$port" \
		DUSTPAN_TEST_S3_BUCKET=bench \
		DUSTPAN_TEST_S3_ACCESS_KEY="$(cat "$SECRETS/$store-access")" \
		DUSTPAN_TEST_S3_SECRET_KEY="$(cat "$SECRETS/$store-secret")" \
		DUSTPAN_TEST_POSTGRES_URL="postgres://bench:$(cat "$SECRETS/pg-password")@127.0.0.1:$PG_PORT/bench?sslmode=disable" \
		"$WORK/load" -flows blobs -blobs s3 -s3-probe="$probe" -namespace bench \
		-reps "$reps" -dataset-units "$dataset" -max-duration "$duration" -http-timeout 3600 \
		-out "$OUT_DIR/load/$step-$store.json" 2>"$OUT_DIR/load/$step-$store.log"; then
		echo "bench-s3: load harness failed for $step on $store; see $OUT_DIR/load/$step-$store.log" >&2
	fi
}

empty_minio_trash &
TRASH_PID=$!

echo "bench-s3: set up the proxies and the buckets"
bench setup

# The protocol mode runs the old path and the new path of each blob family
# with a fixed count of operations, in alternating runs, for each profile.
# A run that stops keeps protocol.json, and the next run with the same
# BENCH_S3_OUT skips the results that it holds.
if [ "${BENCH_S3_MODE:-}" = protocol ]; then
	for step in ${BENCH_S3_PROTOCOL_PROFILES:-same-az cross-az eu-metro}; do
		echo "bench-s3: protocol $step"
		bench protocol -profile "$step" </dev/null
	done
	bench reset
	echo "bench-s3: done; read $OUT_DIR/protocol.md"
	exit 0
fi

bench profiles >"$WORK/steps"

while read -r step kind stores reps dataset duration; do
	echo "bench-s3: $kind $step"
	bench measure -profile "$step" </dev/null
	for store in ${stores//,/ }; do
		run_load "$step" "$store" "$reps" "$dataset" "$duration" </dev/null
	done
done <"$WORK/steps"

bench reset
bench render
echo "bench-s3: done; read $OUT_DIR/results.md"
