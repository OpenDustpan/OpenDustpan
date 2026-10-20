#!/usr/bin/env bash
# Type-check each TypeScript client with strict flags and run its smoke test
# against a fresh fake server. Usage: smoke-ts.sh <generated dir> <work dir> <port>
set -u
GEN=$(realpath "$1"); WORK=$(realpath -m "$2"); PORT=$3
HERE=$(dirname "$(realpath "$0")")/..
NODE=$HERE/scripts/node
for pair in heyapi:ts-hey-api openapifetch:ts-openapi-typescript tsfetch:ts-openapi-generator; do
  kind=${pair%%:*}; dir=${pair#*:}
  for v in 3.0 3.1; do
    m=$WORK/$dir-$v; rm -rf "$m"; mkdir -p "$m"
    cp -r "$GEN/$dir-$v" "$m/client"; cp "$HERE"/transport/ts/transport.ts "$HERE"/transport/ts/env.d.ts "$HERE/transport/ts/smoke-$kind.ts" "$m/"
    ln -s "$NODE/node_modules" "$m/node_modules"
    cat > "$m/tsconfig.json" <<JSON
{"compilerOptions":{"target":"es2022","module":"esnext","moduleResolution":"bundler","strict":true,
"noUncheckedIndexedAccess":true,"exactOptionalPropertyTypes":false,"skipLibCheck":true,"outDir":"out","types":[],
"lib":["es2023","dom","dom.iterable"]},"include":["*.ts","client/**/*.ts"]}
JSON
    echo "== $dir-$v"
    (cd "$m" && echo '{"type":"module"}' > package.json && "$NODE/node_modules/.bin/tsc" -p . 2>&1 | head -8)
    node "$HERE/scripts/fix-esm-imports.mjs" "$m/out"
    python3 -I "$HERE/transport/fakeserver.py" "$PORT" & srv=$!; sleep 0.5
    (cd "$m" && node "out/smoke-$kind.js" "http://127.0.0.1:$PORT" 2>&1 | cut -c1-240)
    kill "$srv"; wait "$srv" 2>/dev/null
  done
done
