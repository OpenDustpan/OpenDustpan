#!/usr/bin/env bash
# Run each candidate generator on the fixture, in 3.0 and 3.1.
# Usage: generate.sh <fixture dir> <out dir> <openapi-generator jar> <node workspace>
# The node workspace is scripts/node (npm ci there first). It pins TypeScript
# 5.9.3: npx alone picks TypeScript 7, and @hey-api/openapi-ts 0.99.0 then fails.
# Each run writes <out>/<lang>-<tool>-<oas>/ and <out>/logs/<same>.log.
# Every tool version is pinned. A failed run is logged, not fatal.
set -u
FIX=$(realpath "$1"); OUT=$(realpath -m "$2"); JAR=$(realpath "$3"); BIN=$(realpath "$4")/node_modules/.bin
mkdir -p "$OUT/logs"
HERE=$(dirname "$(realpath "$0")")

OAPI_CODEGEN=github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
OGEN=github.com/ogen-go/ogen/cmd/ogen@v1.24.0
OPC=openapi-python-client==0.29.1

run() { # name, command...
  local name=$1; shift
  rm -rf "${OUT:?}/$name"; mkdir -p "$OUT/$name"
  if "$@" >"$OUT/logs/$name.log" 2>&1; then echo "ok   $name"; else echo "FAIL $name (exit $?)"; fi
}

for v in 3.0 3.1; do
  spec=$FIX/openapi-$v.json
  run go-oapi-codegen-$v go run $OAPI_CODEGEN -config "$HERE/oapi-codegen.yaml" -o "$OUT/go-oapi-codegen-$v/client.gen.go" "$spec"
  run go-ogen-$v go run $OGEN --config "$HERE/ogen.yaml" --target "$OUT/go-ogen-$v" --package dustpan --clean "$spec"
  run go-openapi-generator-$v java -jar "$JAR" generate -g go -i "$spec" -o "$OUT/go-openapi-generator-$v" \
    --additional-properties=packageName=dustpan,withGoMod=false,enumClassPrefix=true,generateInterfaces=true \
    --global-property=apiTests=false,modelTests=false
  run python-openapi-generator-$v java -jar "$JAR" generate -g python -i "$spec" -o "$OUT/python-openapi-generator-$v" \
    --additional-properties=packageName=opendustpan,projectName=opendustpan --global-property=apiTests=false,modelTests=false
  run python-openapi-python-client-$v uvx --from $OPC openapi-python-client generate --path "$spec" --config "$HERE/openapi-python-client.yaml" \
    --output-path "$OUT/python-openapi-python-client-$v" --overwrite --meta uv
  run ts-openapi-generator-$v java -jar "$JAR" generate -g typescript-fetch -i "$spec" -o "$OUT/ts-openapi-generator-$v" \
    --additional-properties=npmName=@opendustpan/client,supportsES6=true,withInterfaces=true
  run ts-openapi-typescript-$v "$BIN/openapi-typescript" "$spec" -o "$OUT/ts-openapi-typescript-$v/schema.d.ts"
  run ts-hey-api-$v "$BIN/openapi-ts" -i "$spec" -o "$OUT/ts-hey-api-$v" -c @hey-api/client-fetch
done
# Only a quality sample of the other languages, 3.0 only.
run csharp-openapi-generator-3.0 java -jar "$JAR" generate -g csharp -i "$FIX/openapi-3.0.json" -o "$OUT/csharp-openapi-generator-3.0" \
  --additional-properties=packageName=OpenDustpan.Client,targetFramework=net8.0,library=generichost --global-property=apiTests=false,modelTests=false
run java-openapi-generator-3.0 java -jar "$JAR" generate -g java -i "$FIX/openapi-3.0.json" -o "$OUT/java-openapi-generator-3.0" \
  --additional-properties=library=native,groupId=org.opendustpan,artifactId=opendustpan-client,invokerPackage=org.opendustpan.client,apiPackage=org.opendustpan.client.api,modelPackage=org.opendustpan.client.model,openApiNullable=false \
  --global-property=apiTests=false,modelTests=false
