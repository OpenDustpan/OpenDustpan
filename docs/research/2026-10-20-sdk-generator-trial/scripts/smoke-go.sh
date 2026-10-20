#!/usr/bin/env bash
# Build a throwaway module for each Go client and run its smoke test against
# the fake server. Usage: smoke-go.sh <generated dir> <work dir>
set -u
GEN=$(realpath "$1"); WORK=$(realpath -m "$2")
HERE=$(dirname "$(realpath "$0")")/..
for pair in oapicodegen:go-oapi-codegen ogen:go-ogen openapigen:go-openapi-generator; do
  tag=${pair%%:*}; dir=${pair#*:}
  for v in 3.0 3.1; do
    m=$WORK/$dir-$v; rm -rf "$m"; mkdir -p "$m/client" "$m/transport"
    find "$GEN/$dir-$v" -maxdepth 1 -name '*.go' -exec cp {} "$m/client/" \;
    cp "$HERE"/transport/go/transport.go "$m/transport/"
    cp "$HERE"/transport/go/smoke/*_test.go "$m/"
    (cd "$m" && printf 'module trial\n\ngo 1.27\n' > go.mod && go mod tidy >/dev/null 2>&1 \
      && go vet -tags "$tag" ./... && go test -tags "$tag" -count=1 -v ./... 2>&1 \
      | rg -- '--- |_test\.go:|^(ok|FAIL|PASS)' ) > "$WORK/$dir-$v.txt" 2>&1
    echo "== $dir-$v"; rg -- '--- (PASS|FAIL)|_test.go:' "$WORK/$dir-$v.txt" | sed 's/^\s*//'
  done
done
