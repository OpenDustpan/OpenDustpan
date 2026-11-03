#!/bin/bash
# usage: start.sh <old|new> <sqlite|pg> <run-id>; prints ms from exec to first 200 on /health/ready-like probe
export DUSTPAN_ADMIN_TOKEN="$(head -c 64 /dev/urandom | base64 | tr -d "=+/")"
v=$1; db=$2; id=$3; d=/tmp/sv/$v-$db-$id; rm -rf $d; mkdir -p $d
if [ $db = pg ]; then psql -h 127.0.0.1 -p 5433 -U postgres -qc "drop database if exists sv_${v}_$id" -c "create database sv_${v}_$id template template0 encoding 'UTF8' locale 'C.UTF-8'"; url="postgres://postgres@127.0.0.1:5433/sv_${v}_$id?sslmode=disable"; else url="sqlite:$d/db.sqlite"; fi
port=$((20000 + RANDOM % 20000))
t0=$(date +%s%N)
/tmp/odp-$v serve --database "$url" --listen 127.0.0.1:$port --public-url http://127.0.0.1:$port --key-encryption-key-file /tmp/sv/kek --blob-dir $d/blobs > $d/log 2>&1 &
pid=$!
for i in $(seq 1 2000); do
  code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$port/health/ready 2>/dev/null); [ "$code" = 200 ] && break; kill -0 $pid 2>/dev/null || break; sleep 0.01
done
t1=$(date +%s%N); echo "$v $db first-start ms=$(( (t1-t0)/1000000 )) code=$code"
kill $pid; wait $pid 2>/dev/null
# second start on migrated db
port=$((20000 + RANDOM % 20000)); t0=$(date +%s%N)
/tmp/odp-$v serve --database "$url" --listen 127.0.0.1:$port --public-url http://127.0.0.1:$port --key-encryption-key-file /tmp/sv/kek --blob-dir $d/blobs > $d/log2 2>&1 &
pid=$!
for i in $(seq 1 2000); do
  code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$port/health/ready 2>/dev/null); [ "$code" = 200 ] && break; kill -0 $pid 2>/dev/null || break; sleep 0.01
done
t1=$(date +%s%N); echo "$v $db restart ms=$(( (t1-t0)/1000000 )) code=$code"
kill $pid; wait $pid 2>/dev/null
