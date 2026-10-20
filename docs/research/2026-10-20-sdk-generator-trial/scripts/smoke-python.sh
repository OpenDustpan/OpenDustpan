#!/usr/bin/env bash
# Install each Python client in its own venv (packages no newer than
# 2026-10-06, two weeks before the trial) and run its smoke test against a
# fresh fake server. Usage: smoke-python.sh <generated dir> <work dir> <port>
set -u
GEN=$(realpath "$1"); WORK=$(realpath -m "$2"); PORT=$3
HERE=$(dirname "$(realpath "$0")")/..
mkdir -p "$WORK"
for v in 3.0 3.1; do
  for k in opc:python-openapi-python-client oag:python-openapi-generator; do
    kind=${k%%:*}; d=${k#*:}-$v; venv=$WORK/venv-$d
    rm -rf "$venv"; uv venv -q -p 3.13 "$venv" 2>/dev/null
    VIRTUAL_ENV=$venv uv pip install -q --exclude-newer 2026-10-06 "$GEN/$d" 2>&1 | rg -v UV_NATIVE
    python3 -I "$HERE/transport/fakeserver.py" "$PORT" & srv=$!; sleep 0.5
    echo "== $d"
    (cd "$WORK" && "$venv/bin/python" -I "$HERE/transport/python/smoke.py" "$kind" "http://127.0.0.1:$PORT")
    kill "$srv"; wait "$srv" 2>/dev/null
    echo "deps: $(VIRTUAL_ENV=$venv uv pip freeze 2>/dev/null | rg -v '^opendustpan' | tr '\n' ' ')"
  done
done
